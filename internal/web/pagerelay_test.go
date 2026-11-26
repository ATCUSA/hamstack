package web

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeData2G serves what Data2G's page needs: its own login cookie on /api/session, and an echoing WebSocket at /ws.
// It records the Cookie headers it was sent.
func fakeData2G(t *testing.T) (int, func() []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var mu sync.Mutex
	var cookies []string
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				br := bufio.NewReader(c)
				req, err := http.ReadRequest(br)
				if err != nil {
					return
				}
				mu.Lock()
				cookies = append(cookies, req.Header.Get("Cookie"))
				mu.Unlock()
				switch req.URL.Path {
				case "/ws":
					fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: x\r\n\r\n")
					_, _ = io.Copy(c, br)
				case "/api/session":
					body := `{"ok":true}`
					if !strings.Contains(req.Header.Get("Cookie"), "data2g_session=abc") {
						body = `{"error":"log in"}`
					}
					fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
				default:
					body := "DATA2G PAGE"
					fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), cookies...) }
}

func TestModemPageIsServedOnlyToDashboardLogins(t *testing.T) {
	oldEvery := relayCheckEvery
	relayCheckEvery = 50 * time.Millisecond
	defer func() { relayCheckEvery = oldEvery }()
	port, sent := fakeData2G(t)
	s := New(&fakeBackend{}, Options{PasswordFile: pwFile(t), Logf: func(string, ...any) {}})
	dash := httptest_TLS(t, s)
	page := httptest.NewTLSServer(s.pageRelay(ModemPage{Name: "data2g@hf", Title: "Data2G (hf)", Target: port}, "443"))
	t.Cleanup(page.Close)
	c := clientFor(dash) // a cookie jar: like a browser, it sends the host's cookies to every port of it
	get := func(path string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest("GET", page.URL+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		r, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}

	if code, body := get("/", nil); code != http.StatusUnauthorized || !strings.Contains(body, "Log in to the dashboard") {
		t.Fatalf("without a dashboard login: %d %s", code, body)
	}
	login(t, dash, c, "secret-pass")
	pu, _ := url.Parse(page.URL)
	c.Jar.SetCookies(pu, []*http.Cookie{{Name: "data2g_session", Value: "abc"}}) // the page's own login
	if code, body := get("/", nil); code != http.StatusOK || body != "DATA2G PAGE" {
		t.Fatalf("logged in: %d %s", code, body)
	}
	if code, body := get("/api/session", nil); code != http.StatusOK || !strings.Contains(body, `"ok"`) {
		t.Fatalf("the page's own cookie must reach it: %d %s", code, body)
	}
	for _, ck := range sent() {
		if strings.Contains(ck, cookieName) {
			t.Fatalf("the dashboard's login cookie reached the modem: %q", ck)
		}
	}
	req, _ := http.NewRequest("POST", page.URL+"/api/settings", strings.NewReader("{}"))
	req.Header.Set("Origin", "https://evil.example")
	if r, err := c.Do(req); err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatalf("a write from another site: %v %v", r, err)
	}

	dialWS := func(origin string) (net.Conn, *bufio.Reader, int) {
		conn, err := tls.Dial("tcp", pu.Host, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		var ck []string
		for _, k := range c.Jar.Cookies(pu) {
			ck = append(ck, k.Name+"="+k.Value)
		}
		fmt.Fprintf(conn, "GET /ws HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", pu.Host, origin, strings.Join(ck, "; "))
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			return conn, br, 0
		}
		return conn, br, resp.StatusCode
	}
	if conn, _, code := dialWS("https://evil.example"); code == http.StatusSwitchingProtocols {
		conn.Close()
		t.Fatal("another site's page must not reach the modem's WebSocket")
	}
	conn, br, code := dialWS(page.URL)
	defer conn.Close()
	if code != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %d", code)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, _ = conn.Write([]byte("hello"))
	buf := make([]byte, 5)
	if _, err := io.ReadFull(br, buf); err != nil || string(buf) != "hello" {
		t.Fatalf("relay: %q %v", buf, err)
	}
	last := sent()[len(sent())-1]
	if !strings.Contains(last, "data2g_session=abc") || strings.Contains(last, cookieName) {
		t.Errorf("the WebSocket gets the page's cookie only: %q", last)
	}
	// Logging out of the dashboard closes the open page's socket.
	lo, _ := http.NewRequest("POST", dash.URL+"/logout", nil)
	lo.Header.Set("Origin", dash.URL)
	if r, err := c.Do(lo); err == nil {
		r.Body.Close()
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := br.ReadByte(); err == nil || os.IsTimeout(err) {
		t.Fatalf("the page's socket stayed open after logging out: %v", err)
	}
}

func TestWithoutCookie(t *testing.T) {
	if got := withoutCookie("a=1; hamstack_session=x; data2g_session=y", "hamstack_session"); got != "a=1; data2g_session=y" {
		t.Errorf("%q", got)
	}
	if got := withoutCookie("hamstack_session=x", "hamstack_session"); got != "" {
		t.Errorf("%q", got)
	}
}
