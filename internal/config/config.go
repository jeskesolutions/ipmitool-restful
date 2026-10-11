package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

type Config struct {
	Server   Server   `mapstructure:"server"`
	Auth     Auth     `mapstructure:"auth"`
	IPMITool IPMITool `mapstructure:"ipmitool"`
}

type Server struct {
	Address    string   `mapstructure:"address"`
	TLS        TLS      `mapstructure:"tls"`
	AllowedIPs []string `mapstructure:"allowed_ips"`
}

type TLS struct {
	Enabled  bool   `mapstructure:"enabled"`
	CertFile string `mapstructure:"cert_file"`
	KeyFile  string `mapstructure:"key_file"`
}

type Auth struct {
	Token string `mapstructure:"token"`
}

type IPMITool struct {
	Binary         string        `mapstructure:"binary"`
	Timeout        time.Duration `mapstructure:"timeout"`
	MaxOutputBytes int           `mapstructure:"max_output_bytes"`
	MaxConcurrent  int           `mapstructure:"max_concurrent"`
}

func Load(path string) (Config, error) {
	v := viper.New()
	v.SetDefault("server.address", "127.0.0.1:8080")
	v.SetDefault("server.tls.enabled", false)
	v.SetDefault("server.tls.cert_file", "")
	v.SetDefault("server.tls.key_file", "")
	v.SetDefault("server.allowed_ips", []string{})
	v.SetDefault("auth.token", "")
	v.SetDefault("ipmitool.binary", "ipmitool")
	v.SetDefault("ipmitool.timeout", "30s")
	v.SetDefault("ipmitool.max_output_bytes", 1<<20)
	v.SetDefault("ipmitool.max_concurrent", 4)
	if err := readConfigFile(v, path); err != nil {
		return Config{}, err
	}
	if err := applyEnvironment(v); err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := v.UnmarshalExact(&cfg, func(decoder *mapstructure.DecoderConfig) {
		decoder.WeaklyTypedInput = false
	}); err != nil {
		return Config{}, errors.New("invalid configuration: check field names and value types")
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func readConfigFile(v *viper.Viper, path string) error {
	filename := path
	if filename == "" {
		filename = "config.yaml"
	}
	data, err := os.ReadFile(filename)
	if path == "" && errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot read configuration file: check its path and syntax")
	}
	codec, err := viper.NewCodecRegistry().Decoder(strings.TrimPrefix(filepath.Ext(filename), "."))
	if err != nil {
		return errors.New("cannot read configuration file: unsupported file format")
	}
	values := make(map[string]any)
	if err := codec.Decode(data, values); err != nil {
		return errors.New("cannot read configuration file: check its path and syntax")
	}
	// Validate before Viper drops nulls or converts values using defaults.
	var fileConfig Config
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:      &fileConfig,
		ErrorUnused: true,
		DecodeNil:   true,
		DecodeHook:  strictFileValue,
	})
	if err != nil {
		return errors.New("cannot initialize configuration decoder")
	}
	if err := decoder.Decode(values); err != nil {
		return errors.New("invalid configuration: check field names and value types")
	}
	return v.MergeConfigMap(values)
}

func strictFileValue(from, to reflect.Value) (any, error) {
	if !from.IsValid() || (from.Kind() == reflect.Map || from.Kind() == reflect.Slice) && from.IsNil() {
		return nil, errors.New("configuration values must not be null")
	}
	if from.Kind() == reflect.Map {
		for _, key := range from.MapKeys() {
			if from.MapIndex(key).Interface() == nil {
				return nil, errors.New("configuration values must not be null")
			}
		}
	}
	if from.Kind() == reflect.Slice {
		for i := 0; i < from.Len(); i++ {
			if from.Index(i).Interface() == nil {
				return nil, errors.New("configuration values must not be null")
			}
		}
	}
	if to.Type() == reflect.TypeFor[time.Duration]() {
		if from.Kind() != reflect.String {
			return nil, errors.New("duration must be a string with a unit")
		}
		return time.ParseDuration(from.String())
	}
	if to.Kind() == reflect.Int && from.Kind() != reflect.Int && from.Kind() != reflect.Int64 {
		return nil, errors.New("expected an integer")
	}
	return from.Interface(), nil
}

func applyEnvironment(v *viper.Viper) error {
	for _, key := range []string{
		"server.address", "auth.token", "ipmitool.binary", "ipmitool.timeout",
		"ipmitool.max_output_bytes", "ipmitool.max_concurrent",
		"server.tls.enabled", "server.tls.cert_file", "server.tls.key_file", "server.allowed_ips",
	} {
		env := "IPMI_API_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		value, exists := os.LookupEnv(env)
		if !exists {
			continue
		}
		switch key {
		case "ipmitool.max_output_bytes", "ipmitool.max_concurrent":
			number, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("%s must be an integer", env)
			}
			v.Set(key, number)
		case "server.tls.enabled":
			if value != "true" && value != "false" {
				return fmt.Errorf("%s must be true or false", env)
			}
			v.Set(key, value == "true")
		case "server.allowed_ips":
			addresses := []string{}
			if value != "" {
				addresses = strings.Split(value, ",")
				for i := range addresses {
					addresses[i] = strings.TrimSpace(addresses[i])
				}
			}
			v.Set(key, addresses)
		default:
			v.Set(key, value)
		}
	}
	return nil
}

func (c Config) validate() error {
	if err := validateAuth(c.Auth); err != nil {
		return err
	}
	if err := validateServer(c.Server); err != nil {
		return err
	}
	return validateIPMITool(c.IPMITool)
}

var bearerToken = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)

func validateAuth(auth Auth) error {
	if !bearerToken.MatchString(auth.Token) {
		return errors.New("auth.token must be a nonempty Bearer token using letters, digits, -._~+/ and optional trailing =")
	}
	return nil
}

func validateServer(server Server) error {
	host, port, err := net.SplitHostPort(server.Address)
	if err != nil || !validListenHost(host) {
		return errors.New("server.address must be a host:port address")
	}
	if strings.HasPrefix(server.Address, "[") {
		if _, err := netip.ParseAddr(host); err != nil {
			return errors.New("server.address brackets require an IP address")
		}
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("server.address port must be between 0 and 65535")
	}
	for _, value := range server.AllowedIPs {
		address, addrErr := netip.ParseAddr(value)
		_, prefixErr := netip.ParsePrefix(value)
		if (addrErr != nil || address.Zone() != "") && prefixErr != nil {
			return errors.New("server.allowed_ips must contain only IP addresses or CIDR prefixes without zones")
		}
	}
	if strings.ContainsFunc(server.TLS.CertFile, unicode.IsControl) || strings.ContainsFunc(server.TLS.KeyFile, unicode.IsControl) {
		return errors.New("TLS certificate and key paths must not contain control characters")
	}
	if server.TLS.Enabled && (strings.TrimSpace(server.TLS.CertFile) == "" || strings.TrimSpace(server.TLS.KeyFile) == "") {
		return errors.New("server.tls.cert_file and server.tls.key_file are required when TLS is enabled")
	}
	return nil
}

func validListenHost(host string) bool {
	if host == "" {
		return true
	}
	if address, err := netip.ParseAddr(host); err == nil {
		return !strings.ContainsAny(address.Zone(), "/\\[]:%") && !strings.ContainsFunc(address.Zone(), func(r rune) bool {
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
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func validateIPMITool(tool IPMITool) error {
	if strings.TrimSpace(tool.Binary) == "" || strings.ContainsFunc(tool.Binary, unicode.IsControl) {
		return errors.New("ipmitool.binary must be a nonempty executable name or path without control characters")
	}
	if tool.Timeout <= 0 || tool.Timeout > 24*time.Hour {
		return errors.New("ipmitool.timeout must be greater than zero and no more than 24h")
	}
	if tool.MaxOutputBytes <= 0 || tool.MaxOutputBytes > 64<<20 {
		return errors.New("ipmitool.max_output_bytes must be between 1 and 67108864")
	}
	if tool.MaxConcurrent <= 0 || tool.MaxConcurrent > 1024 {
		return errors.New("ipmitool.max_concurrent must be between 1 and 1024")
	}
	return nil
}
