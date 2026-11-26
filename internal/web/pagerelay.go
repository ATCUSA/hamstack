package web

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Some modems serve a web page of their own (Data2G's: waterfall, settings, PTT) at the root of a port, with no TLS,
// and their scripts use root paths ("/api/session", "/ws"), so it cannot be moved under a path of the dashboard. The
// dashboard serves each such page on a port of its own instead: HTTPS with the dashboard's certificate, on the
// dashboard's address, and only to a browser logged in to the dashboard. A browser sends a host's cookies to every
// port of it, so the dashboard login is seen there with nothing more to do. The modem itself then listens on this
// computer only.

// ModemPage is one such page: Port is where the dashboard serves it, Target the modem's own port on localhost.
type ModemPage struct {
	Name   string // "data2g@hf"
	Title  string // "Data2G (hf)"
	Port   int
	Target int
}

// ModemPages is implemented by a backend whose modems have pages to serve this way.
type ModemPages interface {
	ModemPages() []ModemPage
}

// pageRelayEvery is how often the relays follow the setup (a page turned on or off, a rig added).
var pageRelayEvery = 10 * time.Second

// withoutCookie is a Cookie header with one cookie taken out.
func withoutCookie(header, name string) string {
	var keep []string
	for _, c := range strings.Split(header, ";") {
		c = strings.TrimSpace(c)
		if c == "" || strings.HasPrefix(c, name+"=") {
			continue
		}
		keep = append(keep, c)
	}
	return strings.Join(keep, "; ")
}

func isUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// pageRelay is the handler for one modem page.
func (s *Server) pageRelay(p ModemPage, dashPort string) http.Handler {
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(p.Target)}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = target.Host
			if c := withoutCookie(pr.In.Header.Get("Cookie"), cookieName); c != "" {
				pr.Out.Header.Set("Cookie", c)
			} else {
				pr.Out.Header.Del("Cookie")
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, p.Title+"'s page is not answering: is the modem running?", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.loggedIn(r) {
			host, _, err := net.SplitHostPort(r.Host)
			if err != nil {
				host = r.Host
			}
			dash := "https://" + net.JoinHostPort(host, dashPort) + "/"
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><meta name=viewport content=\"width=device-width\"><title>%s</title>"+
				"<p>%s's page is shown to the hamstack dashboard's logins only. <a href=\"%s\">Log in to the dashboard</a>, then come back to this page.</p>",
				html.EscapeString(p.Title), html.EscapeString(p.Title), html.EscapeString(dash))
			return
		}
		if (r.Method != http.MethodGet && r.Method != http.MethodHead || isUpgrade(r)) && (!sameOrigin(r) || isUpgrade(r) && r.Header.Get("Origin") == "") {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		if isUpgrade(r) {
			// Through the dashboard's own relay, so the socket closes when the login ends.
			s.relayWS(w, r, p.Target, p.Title, r.URL.RequestURI(), withoutCookie(r.Header.Get("Cookie"), cookieName))
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// pageRelays starts and stops the relays as the backend's pages come and go, until ctx ends.
func (s *Server) pageRelays(ctx context.Context, host, dashPort string, tlsCfg *tls.Config) {
	be, ok := s.be.(ModemPages)
	if !ok {
		return
	}
	var mu sync.Mutex
	running := map[string]*http.Server{} // by "name:port:target"
	follow := func() {
		want := map[string]ModemPage{}
		for _, p := range be.ModemPages() {
			want[fmt.Sprintf("%s:%d:%d", p.Name, p.Port, p.Target)] = p
		}
		mu.Lock()
		defer mu.Unlock()
		for k, srv := range running {
			if _, ok := want[k]; !ok {
				_ = srv.Close()
				delete(running, k)
			}
		}
		for k, p := range want {
			if _, ok := running[k]; ok {
				continue
			}
			srv := &http.Server{Addr: net.JoinHostPort(host, strconv.Itoa(p.Port)), Handler: s.pageRelay(p, dashPort), TLSConfig: tlsCfg,
				ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute, ErrorLog: s.serverErrorLog()}
			l, err := net.Listen("tcp", srv.Addr)
			if err != nil {
				s.opt.Logf("dashboard: cannot serve %s's page on port %d: %v", p.Title, p.Port, err)
				continue
			}
			running[k] = srv
			s.opt.Logf("dashboard: %s's page on https://%s", p.Title, srv.Addr)
			go func() {
				if err := srv.ServeTLS(l, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
					s.opt.Logf("dashboard: %s's page: %v", p.Title, err)
				}
			}()
		}
	}
	follow()
	t := time.NewTicker(pageRelayEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			for _, srv := range running {
				_ = srv.Close()
			}
			mu.Unlock()
			return
		case <-t.C:
			follow()
		}
	}
}
