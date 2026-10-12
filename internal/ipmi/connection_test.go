package ipmi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRunRejectsInvalidConnections(t *testing.T) {
	c := fakeClient(t, "printf 'should not execute'\n", time.Second, 1024)
	for _, tc := range []struct {
		name   string
		change func(*Connection)
	}{
		{"local interface", func(c *Connection) { c.Interface = "open" }},
		{"unknown interface", func(c *Connection) { c.Interface = "unknown" }},
		{"missing host", func(c *Connection) { c.Host = "" }},
		{"NUL host", func(c *Connection) { c.Host = "bad\x00host" }},
		{"host with port", func(c *Connection) { c.Host = "192.0.2.1:623" }},
		{"URL host", func(c *Connection) { c.Host = "https://bmc.example.com" }},
		{"missing username", func(c *Connection) { c.Username = "" }},
		{"blank username", func(c *Connection) { c.Username = " \t" }},
		{"NUL username", func(c *Connection) { c.Username = "bad\x00user" }},
		{"control in username", func(c *Connection) { c.Username = "admin\n" }},
		{"long username", func(c *Connection) { c.Username = strings.Repeat("a", 17) }},
		{"long UTF-8 username", func(c *Connection) { c.Username = strings.Repeat("é", 9) }},
		{"NUL password", func(c *Connection) { c.Password = "private-password\x00" }},
		{"long lan password", func(c *Connection) { c.Interface = "lan"; c.Password = strings.Repeat("a", 17) }},
		{"long lanplus password", func(c *Connection) { c.Password = strings.Repeat("a", 21) }},
		{"long UTF-8 password", func(c *Connection) { c.Password = strings.Repeat("é", 11) }},
		{"NUL privilege", func(c *Connection) { c.Privilege = "bad\x00privilege" }},
		{"unknown privilege", func(c *Connection) { c.Privilege = "ROOT" }},
		{"privilege with whitespace", func(c *Connection) { c.Privilege = " USER" }},
		{"privilege suffix without name", func(c *Connection) { c.Privilege = "+" }},
		{"repeated privilege suffix", func(c *Connection) { c.Privilege = "USER++" }},
		{"NUL auth type", func(c *Connection) { c.AuthType = "bad\x00auth" }},
		{"unknown auth type", func(c *Connection) { c.AuthType = "SHA256" }},
		{"large password", func(c *Connection) { c.Password = strings.Repeat("x", maxInputSize) }},
		{"invalid port", func(c *Connection) { c.Port = 65536 }},
		{"negative port", func(c *Connection) { c.Port = -1 }},
		{"negative cipher suite", func(c *Connection) { value := -1; c.CipherSuite = &value }},
		{"large cipher suite", func(c *Connection) { value := 256; c.CipherSuite = &value }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := testConnection()
			tc.change(&conn)
			result, err := c.Run(context.Background(), conn, []string{"sdr"})
			if !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("expected invalid connection, got %v", err)
			}

			if result.Stdout != "" {
				t.Fatal("invalid connection was executed")
			}

			if strings.Contains(err.Error(), "private-password") {
				t.Fatal("validation error leaked password")
			}
		})
	}
}

func TestConnectionAcceptsNativeValuesAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Connection)
	}{
		{"username limit", func(c *Connection) { c.Username = strings.Repeat("é", 8) }},
		{"lan password limit", func(c *Connection) { c.Interface = "lan"; c.Password = strings.Repeat("é", 8) }},
		{"lanplus password limit", func(c *Connection) { c.Password = strings.Repeat("é", 10) }},
		{"empty password", func(c *Connection) { c.Password = "" }},
		{"password with controls", func(c *Connection) { c.Password = "a\nb\t" }},
		{"minimum port", func(c *Connection) { c.Port = 1 }},
		{"maximum port", func(c *Connection) { c.Port = 65535 }},
		{"minimum cipher suite", func(c *Connection) { value := 0; c.CipherSuite = &value }},
		{"maximum cipher suite", func(c *Connection) { value := 255; c.CipherSuite = &value }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := testConnection()
			tc.change(&conn)
			if err := conn.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}

	for _, privilege := range []string{"CALLBACK", "USER", "OPERATOR", "ADMINISTRATOR", "OEM", "NO ACCESS"} {
		for _, value := range []string{privilege, strings.ToLower(privilege), privilege + "+"} {
			conn := testConnection()
			conn.Privilege = value
			if err := conn.Validate(); err != nil {
				t.Errorf("native privilege %q rejected: %v", value, err)
			}
		}
	}

	for _, auth := range []string{"NONE", "MD2", "MD5", "PASSWORD", "OEM", "RMCP+"} {
		for _, value := range []string{auth, strings.ToLower(auth)} {
			conn := testConnection()
			conn.AuthType = value
			if err := conn.Validate(); err != nil {
				t.Errorf("native auth type %q rejected: %v", value, err)
			}
		}
	}
}

func TestValidHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"192.0.2.5", true},
		{"2001:db8::5", true},
		{"fe80::1%eth0", true},
		{"fe80::1%2", true},
		{"bmc", true},
		{"bmc-01.example.com", true},
		{"bmc.example.com.", true},
		{"", false},
		{" ", false},
		{"bmc\n.example.com", false},
		{" bmc.example.com", false},
		{"bmc.example.com/path", false},
		{"bmc.example.com:623", false},
		{"[2001:db8::5]:623", false},
		{"https://bmc.example.com", false},
		{"-H", false},
		{"fe80::1%bad\nzone", false},
		{"fe80::1%eth 0", false},
		{"fe80::1%eth0/path", false},
		{"fe80::1%eth0:623", false},
		{"fe80::1%", false},
		{"bmc..example.com", false},
		{strings.Repeat("a", 64) + ".com", false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			if got := validHost(tc.host); got != tc.want {
				t.Fatalf("validHost(%q) = %t; want %t", tc.host, got, tc.want)
			}
		})
	}
}
