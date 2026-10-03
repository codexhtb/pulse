package main

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func authenticated(next http.Handler, token string, allowedCallers []netip.Addr) http.Handler {
	allowed := make(map[netip.Addr]struct{}, len(allowedCallers))
	for _, addr := range allowedCallers {
		allowed[addr.Unmap()] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		caller, err := netip.ParseAddr(host)
		if err != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		if _, ok := allowed[caller.Unmap()]; !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(token) || subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
