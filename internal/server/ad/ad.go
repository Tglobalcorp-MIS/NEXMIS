// Package ad integrates with Active Directory over LDAP(S). It only ever
// reads from the directory, binds with a low-privilege service account and
// uses paged searches with a minimal attribute set, so it does not burden
// domain controllers.
package ad

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

// RoleMapping maps an AD group to a NEXMIS role.
type RoleMapping struct {
	GroupDN string `json:"group_dn"`
	Role    string `json:"role"`
}

// Settings is stored (with the password encrypted) in the settings table.
type Settings struct {
	Enabled            bool          `json:"enabled"`
	URL                string        `json:"url"`       // ldaps://dc01.corp.local:636 or ldap://...:389
	StartTLS           bool          `json:"start_tls"` // kept for stored settings; ldap:// always uses StartTLS
	BaseDN             string        `json:"base_dn"`
	BindDN             string        `json:"bind_dn"`
	BindPassword       string        `json:"bind_password,omitempty"` // sealed at rest
	ComputerBaseDN     string        `json:"computer_base_dn"`
	UserBaseDN         string        `json:"user_base_dn"`
	Domain             string        `json:"domain"`  // corp.local
	NetBIOS            string        `json:"netbios"` // CORP
	InsecureSkipVerify bool          `json:"insecure_skip_verify"`
	CACertPEM          string        `json:"ca_cert_pem"`
	SyncIntervalMin    int           `json:"sync_interval_min"`
	RoleMappings       []RoleMapping `json:"role_mappings"`
}

// Timeouts keep a slow or unreachable DC from stalling the server.
const (
	dialTimeout   = 8 * time.Second
	searchTimeout = 30 * time.Second
	pageSize      = 500
)

func (s *Settings) dial() (*ldap.Conn, error) {
	if s.URL == "" {
		return nil, errors.New("未設定 LDAP 伺服器位址")
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return nil, fmt.Errorf("LDAP 位址格式錯誤：%w", err)
	}
	tlsCfg := &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: s.InsecureSkipVerify, MinVersion: tls.VersionTLS12}
	if s.CACertPEM != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(s.CACertPEM)) {
			return nil, errors.New("CA 憑證格式錯誤")
		}
		tlsCfg.RootCAs = pool
	}
	conn, err := ldap.DialURL(s.URL, ldap.DialWithTLSConfig(tlsCfg),
		ldap.DialWithDialer(&net.Dialer{Timeout: dialTimeout}))
	if err != nil {
		return nil, fmt.Errorf("無法連線至 %s：%w", s.URL, err)
	}
	conn.SetTimeout(searchTimeout)
	// Binds carry passwords, so plain ldap:// is always upgraded with
	// StartTLS (failing closed if the DC does not offer it).
	if u.Scheme == "ldap" {
		if err := conn.StartTLS(tlsCfg); err != nil {
			conn.Close()
			return nil, fmt.Errorf("StartTLS 失敗：%w", err)
		}
	}
	return conn, nil
}

func (s *Settings) serviceConn() (*ldap.Conn, error) {
	conn, err := s.dial()
	if err != nil {
		return nil, err
	}
	if err := conn.Bind(s.BindDN, s.BindPassword); err != nil {
		conn.Close()
		return nil, fmt.Errorf("服務帳號綁定失敗：%w", err)
	}
	return conn, nil
}

// UserInfo is returned after a successful user bind.
type UserInfo struct {
	Username    string // sAMAccountName
	DisplayName string
	Email       string
	Groups      []string // DNs, including nested membership
}

// ErrInvalidCredentials is returned for a failed user bind.
var ErrInvalidCredentials = errors.New("invalid credentials")

// NormalizeUsername strips DOMAIN\ and @domain forms to sAMAccountName.
func NormalizeUsername(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.LastIndex(u, `\`); i >= 0 {
		u = u[i+1:]
	}
	if i := strings.Index(u, "@"); i >= 0 {
		u = u[:i]
	}
	return u
}

// ErrUserNotFound means the account does not exist or is disabled in AD.
var ErrUserNotFound = errors.New("user not found or disabled")

// findUser looks up an enabled user by sAMAccountName.
func (s *Settings) findUser(conn *ldap.Conn, sam string) (*ldap.Entry, error) {
	base := s.UserBaseDN
	if base == "" {
		base = s.BaseDN
	}
	res, err := conn.Search(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 0, false,
		fmt.Sprintf("(&(objectCategory=person)(objectClass=user)(sAMAccountName=%s)"+
			"(!(userAccountControl:1.2.840.113556.1.4.803:=2)))", ldap.EscapeFilter(sam)),
		[]string{"dn", "sAMAccountName", "displayName", "mail"}, nil))
	if err != nil {
		return nil, fmt.Errorf("查詢使用者失敗：%w", err)
	}
	if len(res.Entries) != 1 {
		return nil, ErrUserNotFound
	}
	return res.Entries[0], nil
}

// userInfo builds UserInfo for an entry, resolving nested group
// membership on the DC (LDAP_MATCHING_RULE_IN_CHAIN).
func (s *Settings) userInfo(conn *ldap.Conn, e *ldap.Entry) (*UserInfo, error) {
	info := &UserInfo{
		Username:    e.GetAttributeValue("sAMAccountName"),
		DisplayName: e.GetAttributeValue("displayName"),
		Email:       e.GetAttributeValue("mail"),
	}
	gres, err := conn.Search(ldap.NewSearchRequest(s.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1000, 0, false,
		fmt.Sprintf("(&(objectClass=group)(member:1.2.840.113556.1.4.1941:=%s))", ldap.EscapeFilter(e.DN)),
		[]string{"dn"}, nil))
	if err != nil {
		return nil, fmt.Errorf("查詢群組失敗：%w", err)
	}
	for _, g := range gres.Entries {
		info.Groups = append(info.Groups, g.DN)
	}
	return info, nil
}

// Authenticate looks the user up with the service account, then verifies
// the password by binding as the user's DN.
func (s *Settings) Authenticate(username, password string) (*UserInfo, error) {
	sam := NormalizeUsername(username)
	if sam == "" || password == "" {
		// An empty password would be an unauthenticated bind, which many
		// servers accept. Never allow it.
		return nil, ErrInvalidCredentials
	}
	conn, err := s.serviceConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	e, err := s.findUser(conn, sam)
	if errors.Is(err, ErrUserNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	uc, err := s.dial()
	if err != nil {
		return nil, err
	}
	defer uc.Close()
	if err := uc.Bind(e.DN, password); err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultInvalidCredentials) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	return s.userInfo(conn, e)
}

// Lookup returns the current directory state of a user without a password,
// for re-checking the authorization of signed-in AD users. It returns
// ErrUserNotFound when the account is gone or disabled.
func (s *Settings) Lookup(username string) (*UserInfo, error) {
	conn, err := s.serviceConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	e, err := s.findUser(conn, NormalizeUsername(username))
	if err != nil {
		return nil, err
	}
	return s.userInfo(conn, e)
}

// RoleFor returns the most privileged role granted by the user's groups,
// or "" when no mapping matches (login is then refused).
func (s *Settings) RoleFor(groups []string, rank func(string) int) string {
	best := ""
	for _, m := range s.RoleMappings {
		for _, g := range groups {
			if strings.EqualFold(strings.TrimSpace(m.GroupDN), g) && rank(m.Role) > rank(best) {
				best = m.Role
			}
		}
	}
	return best
}

// Computer is a computer object from the directory.
type Computer struct {
	Name      string
	DNSName   string
	DN        string
	OS        string
	OSVersion string
	Enabled   bool
	LastLogon *time.Time
}

// ListComputers performs a paged search for computer objects.
func (s *Settings) ListComputers(limit int) ([]Computer, error) {
	conn, err := s.serviceConn()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	base := s.ComputerBaseDN
	if base == "" {
		base = s.BaseDN
	}
	req := ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		"(objectCategory=computer)",
		[]string{"cn", "dNSHostName", "operatingSystem", "operatingSystemVersion", "userAccountControl", "lastLogonTimestamp"}, nil)
	var res *ldap.SearchResult
	if limit > 0 {
		req.SizeLimit = limit
		res, err = conn.Search(req)
		if ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded) {
			err = nil
		}
	} else {
		res, err = conn.SearchWithPaging(req, pageSize)
	}
	if err != nil {
		return nil, fmt.Errorf("查詢電腦物件失敗：%w", err)
	}
	out := make([]Computer, 0, len(res.Entries))
	for _, e := range res.Entries {
		uac, _ := strconv.ParseInt(e.GetAttributeValue("userAccountControl"), 10, 64)
		c := Computer{
			Name:      e.GetAttributeValue("cn"),
			DNSName:   e.GetAttributeValue("dNSHostName"),
			DN:        e.DN,
			OS:        e.GetAttributeValue("operatingSystem"),
			OSVersion: e.GetAttributeValue("operatingSystemVersion"),
			Enabled:   uac&2 == 0,
		}
		if t := FileTime(e.GetAttributeValue("lastLogonTimestamp")); t != nil {
			c.LastLogon = t
		}
		out = append(out, c)
	}
	return out, nil
}

// FileTime converts an AD Windows FILETIME string (100ns since 1601) to time.
func FileTime(v string) *time.Time {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	const epochDiff = 116444736000000000 // 1601-01-01 to 1970-01-01 in 100ns
	t := time.Unix(0, (n-epochDiff)*100).UTC()
	return &t
}

// Test verifies connectivity and the service account, returning a few
// computer names as a sanity check.
func (s *Settings) Test() ([]string, error) {
	comps, err := s.ListComputers(5)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(comps))
	for _, c := range comps {
		names = append(names, c.Name)
	}
	return names, nil
}
