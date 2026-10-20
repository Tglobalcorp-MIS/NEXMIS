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
  # Development only: allows an http:// server. Over cleartext anyone on the
  # network path can replace the agent binary or its enrollment.
  [switch]$AllowHttp,
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
if (-not $Server.StartsWith('https://', [StringComparison]::OrdinalIgnoreCase) -and
    -not ($AllowHttp -and $Server.StartsWith('http://', [StringComparison]::OrdinalIgnoreCase))) {
  Write-Error '-Server 必須以 https:// 開頭。'
  exit 1
}
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
    # The installer request carries the enrollment token and returns code
    # that runs as SYSTEM, so the server must be authenticated exactly as
    # the agent will authenticate it later: by pin, by the given CA, or by
    # the Windows trust store. Any failure inside the callback rejects the
    # connection (fail closed). The check is compiled C#, because a script
    # block callback cannot run on the .NET thread pool in PowerShell 7.
    Add-Type -TypeDefinition @'
    using System;
    using System.Net;
    using System.Net.Security;
    using System.Security.Cryptography;
    using System.Security.Cryptography.X509Certificates;
    public static class NexmisTls {
        public static string Pin;
        public static X509Certificate2 Ca;
        public static bool Insecure;
        public static bool Validate(object sender, X509Certificate cert, X509Chain chain, SslPolicyErrors errors) {
            if (Insecure) return true;
            if (cert == null) return false;
            if (!string.IsNullOrEmpty(Pin)) {
                using (SHA256 sha = SHA256.Create()) {
                    byte[] h = sha.ComputeHash(cert.GetRawCertData());
                    return BitConverter.ToString(h).Replace("-", "").ToLowerInvariant() == Pin;
                }
            }
            if (Ca != null) {
                if ((errors & SslPolicyErrors.RemoteCertificateNameMismatch) != 0) return false;
                X509Chain c = new X509Chain();
                c.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
                c.ChainPolicy.VerificationFlags = X509VerificationFlags.AllowUnknownCertificateAuthority;
                c.ChainPolicy.ExtraStore.Add(Ca);
                if (!c.Build(new X509Certificate2(cert))) return false;
                X509Certificate2 root = c.ChainElements[c.ChainElements.Count - 1].Certificate;
                return root.Thumbprint == Ca.Thumbprint;
            }
            return errors == SslPolicyErrors.None;
        }
    }
'@
    if ($PinSHA256) {
      [NexmisTls]::Pin = ($PinSHA256 -replace '[:\s]', '').ToLowerInvariant()
      if ([NexmisTls]::Pin -notmatch '^[0-9a-f]{64}$') { throw '-PinSHA256 必須是 64 個十六進位字元的 SHA-256 指紋' }
    } elseif ($CAFile) {
      [NexmisTls]::Ca = New-Object Security.Cryptography.X509Certificates.X509Certificate2 -ArgumentList $CAFile
    }
    # -Insecure is for test environments only: the token and the binary are
    # then exposed to anyone on the network path.
    [NexmisTls]::Insecure = [bool]$Insecure -and -not ($PinSHA256 -or $CAFile)
    [Net.ServicePointManager]::ServerCertificateValidationCallback = [Net.Security.RemoteCertificateValidationCallback][NexmisTls]::Validate
    # The token goes in a header so it never appears in proxy or access logs.
    $uri = "$Server/api/agent/v1/installer?arch=$arch"
    Write-Log "下載 Agent（$arch）：$Server"
    $attempt = 0
    while ($true) {
      try {
        # WebClient goes through HttpWebRequest, which honours the validation
        # callback in both Windows PowerShell and PowerShell 7.
        $wc = New-Object Net.WebClient
        $wc.Headers.Add('X-NEXMIS-Enroll-Token', $Token)
        $wc.DownloadFile($uri, $exe)
        break
      } catch {
        $attempt++
        if ($attempt -ge 4) { throw }
        Write-Log "下載失敗，$([math]::Pow(2, $attempt)) 秒後重試：$($_.Exception.Message)"
        Start-Sleep -Seconds ([math]::Pow(2, $attempt))
      }
    }
  }

  # A signed binary must verify; an unsigned build (v1 releases) is
  # accepted because its origin was established by the TLS checks above.
  $sig = Get-AuthenticodeSignature -FilePath $exe
  Write-Log "數位簽章狀態：$($sig.Status)"
  if ($sig.Status -ne 'Valid' -and $sig.Status -ne 'NotSigned') {
    throw "安裝檔數位簽章無效（$($sig.Status)），已中止"
  }

  $installArgs = @('install', '--server', $Server, '--token', $Token)
  if ($CAFile)    { $installArgs += @('--ca', $CAFile) }
  if ($PinSHA256) { $installArgs += @('--pin', $PinSHA256) }
  if ($Insecure)  { $installArgs += '--insecure' }
  if ($AllowHttp) { $installArgs += '--allow-http' }

  Write-Log '執行 nexmis-agent install'
  & $exe @installArgs 2>&1 | ForEach-Object { Write-Log "  $_" }
  if ($LASTEXITCODE -ne 0) { throw "nexmis-agent install 結束代碼 $LASTEXITCODE" }

  Write-Log '安裝完成，服務 NexmisAgent 已啟動。'
  exit 0
} catch {
  Write-Log "安裝失敗：$($_.Exception.Message)"
  exit 1
} finally {
  [Net.ServicePointManager]::ServerCertificateValidationCallback = $null
  Remove-Item -Path $work -Recurse -Force -ErrorAction SilentlyContinue
}
