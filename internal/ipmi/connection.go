package ipmi

import (
	"net/netip"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// Connection contains only the remote BMC settings supplied by one request.
type Connection struct {
	Interface   string
	Host        string
	Port        int
	Username    string
	Password    string
	Privilege   string
	AuthType    string
	CipherSuite *int
}

func (conn Connection) WithDefaults() Connection {
	if conn.Interface == "" {
		conn.Interface = "lanplus"
	}

	if conn.Port == 0 {
		conn.Port = 623
	}

	return conn
}

func (conn Connection) Validate() error {
	conn = conn.WithDefaults()
	fields := []string{conn.Interface, conn.Host, conn.Username, conn.Password, conn.Privilege, conn.AuthType}
	if err := validateStrings(fields); err != nil {
		return err
	}

	if conn.Interface != "lan" && conn.Interface != "lanplus" {
		return invalid("interface must be lan or lanplus")
	}

	if !validHost(conn.Host) {
		return invalid("host must be an IP address or hostname without a port")
	}

	if strings.TrimSpace(conn.Username) == "" {
		return invalid("username is required")
	}

	if strings.ContainsFunc(conn.Username, unicode.IsControl) {
		return invalid("username must not contain control characters")
	}

	if len(conn.Username) > 16 {
		return invalid("username must not exceed 16 bytes")
	}

	if conn.Interface == "lan" && len(conn.Password) > 16 {
		return invalid("password must not exceed 16 bytes for lan")
	}

	if conn.Interface == "lanplus" && len(conn.Password) > 20 {
		return invalid("password must not exceed 20 bytes for lanplus")
	}

	if conn.Privilege != "" {
		switch strings.ToUpper(strings.TrimSuffix(conn.Privilege, "+")) {
		case "CALLBACK", "USER", "OPERATOR", "ADMINISTRATOR", "OEM", "NO ACCESS":
		default:
			return invalid("privilege must be callback, user, operator, administrator, OEM, or no access, optionally followed by +")
		}
	}

	switch strings.ToUpper(conn.AuthType) {
	case "", "NONE", "MD2", "MD5", "PASSWORD", "OEM", "RMCP+":
	default:
		return invalid("auth_type must be none, MD2, MD5, password, OEM, or RMCP+")
	}

	if conn.Port < 1 || conn.Port > 65535 {
		return invalid("port must be between 1 and 65535")
	}

	if conn.CipherSuite != nil && (*conn.CipherSuite < 0 || *conn.CipherSuite > 255) {
		return invalid("cipher_suite must be between 0 and 255")
	}

	return nil
}

func validHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return !strings.ContainsAny(addr.Zone(), "/\\[]:%") && !strings.ContainsFunc(addr.Zone(), func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		})
	}

	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}

	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}

		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}

	return true
}

func (conn Connection) arguments(command []string) []string {
	args := []string{"-I", conn.Interface, "-H", conn.Host, "-U", conn.Username}
	for _, flag := range []struct{ name, value string }{
		{"-L", conn.Privilege}, {"-A", conn.AuthType},
	} {
		if flag.value != "" {
			args = append(args, flag.name, flag.value)
		}
	}

	args = append(args, "-p", strconv.Itoa(conn.Port))
	if conn.CipherSuite != nil {
		args = append(args, "-C", strconv.Itoa(*conn.CipherSuite))
	}

	// Always use the environment, including for an explicitly empty password.
	args = append(args, "-E")
	// GNU getopt otherwise permutes flags that belong to the subcommand.
	args = append(args, "--")
	return append(args, command...)
}

func (conn Connection) environment() []string {
	return append(minimalEnv(), "IPMI_PASSWORD="+conn.Password)
}

func minimalEnv() []string {
	return []string{"PATH=" + os.Getenv("PATH"), "LANG=C"}
}
