package middleware

import (
	"net"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/gin-gonic/gin"

	"ipmitool-microservice/internal/api/response"
)

func AllowIPs(allowed []string) gin.HandlerFunc {
	prefixes := make([]netip.Prefix, 0, len(allowed))
	valid := true
	for _, value := range allowed {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			address, addressErr := netip.ParseAddr(value)
			if addressErr != nil || address.Zone() != "" {
				valid = false
				break
			}

			prefix = netip.PrefixFrom(address, address.BitLen())
		}

		if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}

		prefixes = append(prefixes, prefix)
	}

	return func(c *gin.Context) {
		if len(allowed) == 0 {
			c.Next()
			return
		}

		address, ok := peerAddress(c.Request)
		if valid && ok {
			for _, prefix := range prefixes {
				if prefix.Contains(address) {
					c.Next()
					return
				}
			}
		}

		response.WriteError(c, http.StatusForbidden, "ip_not_allowed", "client IP is not allowed")
	}
}

// Use only the socket peer; forwarding headers cannot grant API access.
func peerAddress(request *http.Request) (netip.Addr, bool) {
	host, port, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}

	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return netip.Addr{}, false
	}

	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}

	return address.WithZone("").Unmap(), true
}
