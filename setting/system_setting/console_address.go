package system_setting

import (
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
)

var ConsoleAddress string

// GetConsoleAddress resolves browser-facing links from trusted administrator
// configuration, never from request headers. Existing installations retain their
// ServerAddress until a separate console origin is configured.
func GetConsoleAddress() string {
	common.OptionMapRWMutex.RLock()
	address := strings.TrimSpace(ConsoleAddress)
	if address == "" {
		address = strings.TrimSpace(ServerAddress)
	}
	common.OptionMapRWMutex.RUnlock()
	return strings.TrimRight(address, "/")
}

func ValidateConsoleAddress(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	invalid := common.NewMessage("Console address must be an HTTPS origin, or an HTTP loopback origin for local development")
	if strings.ContainsAny(raw, "\\?#") || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return invalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return invalid
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return invalid
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return invalid
		}
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if parsed.Scheme == "http" && (strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())) {
		return nil
	}
	return invalid
}
