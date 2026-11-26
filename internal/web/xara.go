package web

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// xara's own web page (waterfall, link state, levels) is served by xara on localhost. hamstack starts it view-only:
// without xara's --ui-control, nobody can key the radio or change settings from it, and it needs no token. It is
// shown here behind the dashboard's login: its files are fetched from xara and its WebSocket relayed like ardopcf's.

// XaraScreens is implemented by a backend that can show xara's page for a rig.
type XaraScreens interface {
	XaraScreen(rig string) (port int, ok bool) // the localhost port of the rig's xara page, while it runs one
}

// xaraFiles are the files xara's page loads, by relative path.
var xaraFiles = map[string]string{"app.js": "/app.js", "common.js": "/common.js", "style.css": "/style.css", "common.css": "/common.css"}

// xaraWSLine is how xara's script opens its WebSocket: at the root of the host. Behind the dashboard it is under the
// rig's path.
const xaraWSLine = "`${proto}//${location.host}/ws`"

func (s *Server) xaraPort(w http.ResponseWriter, r *http.Request) (int, bool) {
	be, ok := s.be.(XaraScreens)
	if !ok {
		http.NotFound(w, r)
		return 0, false
	}
	port, ok := be.XaraScreen(r.PathValue("rig"))
	if !ok {
		http.Error(w, "this rig is not running xara with its screen turned on", http.StatusNotFound)
		return 0, false
	}
	return port, true
}

// xaraFile serves the page ("" for index.html) or one of its files from the rig's xara.
func (s *Server) xaraFile(w http.ResponseWriter, r *http.Request) {
	path := "/"
	if f := r.PathValue("file"); f != "" {
		p, ok := xaraFiles[f]
		if !ok {
			http.NotFound(w, r)
			return
		}
		path = p
	}
	port, ok := s.xaraPort(w, r)
	if !ok {
		return
	}
	req, _ := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	cl := http.Client{Timeout: 5 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		http.Error(w, "xara's screen is not answering: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		http.Error(w, "xara's screen did not send "+path, http.StatusBadGateway)
		return
	}
	if path == "/common.js" {
		text := string(body)
		if !strings.Contains(text, xaraWSLine) {
			s.opt.Logf("dashboard: xara's script has changed (no %s): its screen may not connect", xaraWSLine)
		}
		text = strings.ReplaceAll(text, xaraWSLine, "`${proto}//${location.host}/xara/"+r.PathValue("rig")+"/ws`")
		body = []byte(text)
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// xaraWS relays the page's WebSocket to the rig's xara.
func (s *Server) xaraWS(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) || r.Header.Get("Origin") == "" {
		http.Error(w, "cross-origin WebSocket refused", http.StatusForbidden)
		return
	}
	port, ok := s.xaraPort(w, r)
	if !ok {
		return
	}
	s.relayScreenWS(w, r, port, "xara")
}
