package web

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeArdop serves what ardopcf's own web page serves: "/", "/webgui.js" (whose WebSocket line is the real one)
// and a WebSocket at /ws that echoes, on a plain TCP port.
func fakeArdop(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
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
				switch req.URL.Path {
				case "/":
					body := `<script src="webgui.js"></script>ARDOP`
					fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: text/html\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
				case "/webgui.js":
					body := `WebSocketClient.init("ws://" + document.location.host + "/ws");`
					fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: text/javascript\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
				case "/ws":
					if req.Header.Get("Upgrade") != "websocket" {
						fmt.Fprintf(c, "HTTP/1.1 400 Bad Request\r\n\r\n")
						return
					}
					fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: x\r\n\r\n")
					_, _ = io.Copy(c, br)
				default:
					fmt.Fprintf(c, "HTTP/1.1 404 Not Found\r\n\r\n")
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

type ardopBackend struct {
	fakeBackend
	port int
}

func (a *ardopBackend) ArdopScreen(rig string) (int, bool) { return a.port, rig == "ftdx10" }

func TestArdopScreenIsServedOnlyThroughTheDashboard(t *testing.T) {
	be := &ardopBackend{port: fakeArdop(t)}
	ts := httptest_TLS(t, New(be, Options{PasswordFile: pwFile(t), Logf: func(string, ...any) {}}))
	c := clientFor(ts)
	if r, _ := c.Get(ts.URL + "/ardop/ftdx10/"); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("without a login: %d", r.StatusCode)
	}
	login(t, ts, c, "secret-pass")
	r, err := c.Get(ts.URL + "/ardop/ftdx10/")
	if err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("the page: %v %v", r, err)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), "ARDOP") {
		t.Fatalf("page: %s", b)
	}
	r, _ = c.Get(ts.URL + "/ardop/ftdx10/webgui.js")
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	js := string(b)
	if strings.Contains(js, `"ws://" + document.location.host + "/ws"`) || !strings.Contains(js, "/ardop/ftdx10/ws") || !strings.Contains(js, "wss://") {
		t.Fatalf("the script must connect back through the dashboard, over wss on HTTPS: %s", js)
	}
	if r, _ := c.Get(ts.URL + "/ardop/nope/"); r.StatusCode != http.StatusNotFound {
		t.Errorf("a rig with no screen: %d", r.StatusCode)
	}

	// The WebSocket, through the dashboard, with the login's cookie and this site as origin.
	u, _ := url.Parse(ts.URL)
	dialWS := func(origin string) (net.Conn, *bufio.Reader, int) {
		conn, err := tls.Dial("tcp", u.Host, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		var ck []string
		for _, k := range c.Jar.Cookies(u) {
			ck = append(ck, k.Name+"="+k.Value)
		}
		fmt.Fprintf(conn, "GET /ardop/ftdx10/ws HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", u.Host, origin, strings.Join(ck, "; "))
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			return conn, br, 0
		}
		return conn, br, resp.StatusCode
	}
	if conn, _, code := dialWS("https://evil.example"); code == http.StatusSwitchingProtocols {
		conn.Close()
		t.Fatal("another site's page must not reach ardopcf's screen")
	}
	conn, br, code := dialWS(ts.URL)
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
}

func pwFile(t *testing.T) string {
	t.Helper()
	p := t.TempDir() + "/pw"
	h, _ := HashPassword("secret-pass")
	if err := SavePasswordHash(p, h); err != nil {
		t.Fatal(err)
	}
	return p
}
