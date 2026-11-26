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

// fakeXara serves what xara's view-only page serves: "/", its scripts (common.js opens the WebSocket the way xara's
// does) and an echoing WebSocket at /ws. It answers only requests whose Host is 127.0.0.1, as xara refuses others.
func fakeXara(t *testing.T) int {
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
				if !strings.HasPrefix(req.Host, "127.0.0.1") {
					fmt.Fprintf(c, "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
					return
				}
				reply := func(ctype, body string) {
					fmt.Fprintf(c, "HTTP/1.1 200 OK\r\nContent-Type: %s\r\nContent-Length: %d\r\n\r\n%s", ctype, len(body), body)
				}
				switch req.URL.Path {
				case "/":
					reply("text/html", `<script src="common.js"></script><script src="app.js"></script>XARA`)
				case "/common.js":
					reply("text/javascript", "const proto = location.protocol === \"https:\" ? \"wss:\" : \"ws:\";\nws = new WebSocket(`${proto}//${location.host}/ws`);")
				case "/app.js":
					reply("text/javascript", "// app")
				case "/ws":
					fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: x\r\n\r\n")
					_, _ = io.Copy(c, br)
				default:
					fmt.Fprintf(c, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n")
				}
			}()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

type xaraBackend struct {
	fakeBackend
	port int
}

func (x *xaraBackend) XaraScreen(rig string) (int, bool) { return x.port, rig == "ftdx10" }

func TestXaraScreenIsServedOnlyThroughTheDashboard(t *testing.T) {
	be := &xaraBackend{port: fakeXara(t)}
	ts := httptest_TLS(t, New(be, Options{PasswordFile: pwFile(t), Logf: func(string, ...any) {}}))
	c := clientFor(ts)
	if r, _ := c.Get(ts.URL + "/xara/ftdx10/"); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("without a login: %d", r.StatusCode)
	}
	login(t, ts, c, "secret-pass")
	get := func(path string) (int, string) {
		r, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if code, body := get("/xara/ftdx10/"); code != http.StatusOK || !strings.Contains(body, "XARA") {
		t.Fatalf("the page: %d %s", code, body)
	}
	code, js := get("/xara/ftdx10/common.js")
	if code != http.StatusOK || strings.Contains(js, "${location.host}/ws`") || !strings.Contains(js, "${location.host}/xara/ftdx10/ws`") {
		t.Fatalf("the script must connect back through the dashboard: %d %s", code, js)
	}
	if code, _ := get("/xara/ftdx10/app.js"); code != http.StatusOK {
		t.Errorf("app.js: %d", code)
	}
	if code, _ := get("/xara/ftdx10/secret.txt"); code != http.StatusNotFound {
		t.Errorf("only the page's own files are fetched: %d", code)
	}
	if code, _ := get("/xara/nope/"); code != http.StatusNotFound {
		t.Errorf("a rig with no screen: %d", code)
	}

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
		fmt.Fprintf(conn, "GET /xara/ftdx10/ws HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", u.Host, origin, strings.Join(ck, "; "))
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			return conn, br, 0
		}
		return conn, br, resp.StatusCode
	}
	if conn, _, code := dialWS("https://evil.example"); code == http.StatusSwitchingProtocols {
		conn.Close()
		t.Fatal("another site's page must not reach xara's screen")
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
