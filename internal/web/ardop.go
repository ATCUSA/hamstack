package web

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ardopcf's own web page (spectrum, waterfall, receive levels, drive level, a two-tone test and ID buttons) has no
// password, listens on every interface and checks nothing about who connects. hamstack starts it only while its
// firewall holds the port to this computer, and shows it here, behind the dashboard's login: the page and its
// script are fetched from ardopcf on localhost, and its WebSocket is relayed like a VNC screen (same-origin only,
// closed when the login ends).

// ArdopScreens is implemented by a backend that can show ardopcf's page for a rig.
type ArdopScreens interface {
	ArdopScreen(rig string) (port int, ok bool) // the localhost port of the rig's ardopcf page, when it runs one
}

// ardopWSLine is how ardopcf's script opens its WebSocket: always ws://, always at the root. Behind the dashboard
// it must be wss:// on an HTTPS page and under the rig's path.
const ardopWSLine = `"ws://" + document.location.host + "/ws"`

func (s *Server) ardopPort(w http.ResponseWriter, r *http.Request) (int, bool) {
	be, ok := s.be.(ArdopScreens)
	if !ok {
		http.NotFound(w, r)
		return 0, false
	}
	port, ok := be.ArdopScreen(r.PathValue("rig"))
	if !ok {
		http.Error(w, "this rig is not running ardopcf with its screen turned on", http.StatusNotFound)
		return 0, false
	}
	return port, true
}

// ardopFile serves "/" or "/webgui.js" from the rig's ardopcf.
func (s *Server) ardopFile(file string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		port, ok := s.ardopPort(w, r)
		if !ok {
			return
		}
		cl := http.Client{Timeout: 5 * time.Second}
		resp, err := cl.Get("http://127.0.0.1:" + strconv.Itoa(port) + file)
		if err != nil {
			http.Error(w, "ardopcf's screen is not answering: "+err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil || resp.StatusCode != http.StatusOK {
			http.Error(w, "ardopcf's screen did not send "+file, http.StatusBadGateway)
			return
		}
		if file == "/webgui.js" {
			text := string(body)
			if !strings.Contains(text, ardopWSLine) {
				s.opt.Logf("dashboard: ardopcf's script has changed (no %s): its screen may not connect", ardopWSLine)
			}
			path := "/ardop/" + r.PathValue("rig") + "/ws"
			text = strings.ReplaceAll(text, ardopWSLine, `(location.protocol === "https:" ? "wss://" : "ws://") + document.location.host + "`+path+`"`)
			body = []byte(text)
		}
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

// ardopWS relays the page's WebSocket to the rig's ardopcf.
func (s *Server) ardopWS(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) || r.Header.Get("Origin") == "" {
		http.Error(w, "cross-origin WebSocket refused", http.StatusForbidden)
		return
	}
	ck, err := r.Cookie(cookieName)
	if err != nil {
		http.Error(w, "not logged in", http.StatusUnauthorized)
		return
	}
	key := hashToken(ck.Value)
	port, ok := s.ardopPort(w, r)
	if !ok {
		return
	}
	back, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 5*time.Second)
	if err != nil {
		http.Error(w, "ardopcf's screen is not answering", http.StatusBadGateway)
		return
	}
	defer back.Close()
	// The upgrade request as ardopcf expects it: GET /ws, its own host, and the WebSocket headers only (no cookie).
	var req strings.Builder
	fmt.Fprintf(&req, "GET /ws HTTP/1.1\r\nHost: 127.0.0.1:%d\r\n", port)
	for _, h := range []string{"Upgrade", "Connection", "Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Protocol"} {
		if v := r.Header.Get(h); v != "" {
			fmt.Fprintf(&req, "%s: %s\r\n", h, v)
		}
	}
	req.WriteString("\r\n")
	if _, err := io.WriteString(back, req.String()); err != nil {
		http.Error(w, "ardopcf's screen is not answering", http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot relay a WebSocket here", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	done := make(chan struct{}, 2)
	var typed atomic.Bool
	go func() {
		_, _ = io.Copy(back, activity{io.MultiReader(bufReader(buf.Reader), client), &typed})
		done <- struct{}{}
	}()
	go func() { _, _ = io.Copy(client, back); done <- struct{}{} }()
	tick := time.NewTicker(relayCheckEvery)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			if !s.sessionAlive(key, typed.Swap(false)) {
				return
			}
		}
	}
}

// bufReader is what the server had already read from the connection past the request (nothing, usually).
func bufReader(br *bufio.Reader) io.Reader {
	if n := br.Buffered(); n > 0 {
		b, _ := br.Peek(n)
		return strings.NewReader(string(b))
	}
	return strings.NewReader("")
}
