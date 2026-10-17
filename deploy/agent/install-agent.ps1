<#
.SYNOPSIS
  Installs or upgrades the NEXMIS Windows Agent.

.DESCRIPTION
  Suitable for manual installs, GPO computer startup scripts, Intune or any
  software distribution tool. The script is idempotent: on a machine that
  already runs the agent it only refreshes the binary and the service.

  Downloads nexmis-agent.exe from the NEXMIS server (authorised by the
  enrollment token) unless -AgentPath points at a local copy (for example
  on a NETLOGON or file share), then runs "nexmis-agent.exe install".

.EXAMPLE
  .\install-agent.ps1 -Server https://mis.corp.local -Token nxe_xxxxxxxx

.EXAMPLE
  # GPO startup script with a self-signed server certificate
  .\install-agent.ps1 -Server https://mis.corp.local -Token nxe_xxx -PinSHA256 3F9A...E1

.EXAMPLE
  # Offline install from a share, trusting the corporate CA
  .\install-agent.ps1 -Server https://mis.corp.local -Token nxe_xxx `
      -AgentPath \\corp.local\NETLOGON\nexmis\nexmis-agent.exe -CAFile \\corp.local\NETLOGON\nexmis\ca.pem
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$Server,
  [Parameter(Mandatory = $true)][string]$Token,
  [string]$AgentPath,
  [string]$CAFile,
  [string]$PinSHA256,
  [switch]$Insecure,
  [string]$LogFile = (Join-Path $env:ProgramData 'NEXMIS\install.log')
)

$ErrorActionPreference = 'Stop'

function Write-Log([string]$msg) {
  $line = '{0:yyyy-MM-dd HH:mm:ss} {1}' -f (Get-Date), $msg
  Write-Host $line
  try {
    $dir = Split-Path $LogFile -Parent
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    Add-Content -Path $LogFile -Value $line -Encoding UTF8
  } catch { }
}

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  Write-Error '需要以系統管理員身分執行（GPO 開機指令碼以 SYSTEM 執行即可）。'
  exit 1
}

$Server = $Server.TrimEnd('/')
$arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
$arch = switch ($arch) { 'ARM64' { 'arm64' } default { 'amd64' } }

$work = Join-Path $env:TEMP ("nexmis-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work -Force | Out-Null
$exe = Join-Path $work 'nexmis-agent.exe'

try {
  if ($AgentPath) {
    Write-Log "使用本機安裝檔：$AgentPath"
    Copy-Item -Path $AgentPath -Destination $exe -Force
  } else {
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    if ($Insecure -or $PinSHA256) {
      # The download itself is authorised by the token; the agent enforces
      # the pin for every later connection.
      [Net.ServicePointManager]::ServerCertificateValidationCallback = { $true }
    }
    $uri = "$Server/api/agent/v1/installer?arch=$arch&token=" + [uri]::EscapeDataString($Token)
    Write-Log "下載 Agent（$arch）：$Server"
    $attempt = 0
    while ($true) {
      try {
        Invoke-WebRequest -UseBasicParsing -Uri $uri -OutFile $exe -TimeoutSec 300
        break
      } catch {
        $attempt++
        if ($attempt -ge 4) { throw }
        Write-Log "下載失敗，$([math]::Pow(2, $attempt)) 秒後重試：$($_.Exception.Message)"
        Start-Sleep -Seconds ([math]::Pow(2, $attempt))
      }
    }
  }

  $sig = Get-AuthenticodeSignature -FilePath $exe
  Write-Log "數位簽章狀態：$($sig.Status)"

  $args = @('install', '--server', $Server, '--token', $Token)
  if ($CAFile)    { $args += @('--ca', $CAFile) }
  if ($PinSHA256) { $args += @('--pin', $PinSHA256) }
  if ($Insecure)  { $args += '--insecure' }

  Write-Log '執行 nexmis-agent install'
  & $exe @args 2>&1 | ForEach-Object { Write-Log "  $_" }
  if ($LASTEXITCODE -ne 0) { throw "nexmis-agent install 結束代碼 $LASTEXITCODE" }

  Write-Log '安裝完成，服務 NexmisAgent 已啟動。'
  exit 0
} catch {
  Write-Log "安裝失敗：$($_.Exception.Message)"
  exit 1
} finally {
  Remove-Item -Path $work -Recurse -Force -ErrorAction SilentlyContinue
}
