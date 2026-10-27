package checks

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"hook/internal/store"
)

// fakeRedis answers AUTH/SELECT/PING/INFO like a password-protected server.
func fakeRedis(t *testing.T, password string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				authed := password == ""
				for {
					var args []string
					head, err := r.ReadString('\n')
					if err != nil {
						return
					}
					n := 0
					for _, ch := range strings.TrimSpace(head[1:]) {
						n = n*10 + int(ch-'0')
					}
					for i := 0; i < n; i++ {
						r.ReadString('\n') // $len
						v, _ := r.ReadString('\n')
						args = append(args, strings.TrimRight(v, "\r\n"))
					}
					switch strings.ToUpper(args[0]) {
					case "AUTH":
						if args[len(args)-1] == password {
							authed = true
							c.Write([]byte("+OK\r\n"))
						} else {
							c.Write([]byte("-WRONGPASS invalid username-password pair\r\n"))
						}
					case "PING":
						if !authed {
							c.Write([]byte("-NOAUTH Authentication required.\r\n"))
						} else {
							c.Write([]byte("+PONG\r\n"))
						}
					case "SELECT":
						c.Write([]byte("+OK\r\n"))
					case "INFO":
						body := "# Server\r\nredis_version:7.2.4\r\n"
						c.Write([]byte("$" + itoa(len(body)) + "\r\n" + body + "\r\n"))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

func TestRedis(t *testing.T) {
	addr := fakeRedis(t, "s3cret")
	run := func(dsn string) Result {
		return Run(context.Background(), &store.Monitor{Type: "redis", Timeout: 5, Config: store.MonitorConfig{DSN: dsn}})
	}
	if r := run("redis://:s3cret@" + addr + "/2"); !r.OK || r.Msg != "Redis 7.2.4" {
		t.Fatalf("good password: %+v", r)
	}
	r := run("redis://:wrong@" + addr)
	if r.OK || !strings.Contains(r.Msg, "AUTH failed") {
		t.Fatalf("bad password should fail: %+v", r)
	}
	if r := run("redis://" + addr); r.OK || !strings.Contains(r.Msg, "NOAUTH") {
		t.Fatalf("missing password should fail: %+v", r)
	}
	if r := run("http://" + addr); r.OK {
		t.Fatal("non-redis scheme accepted")
	}
}

func TestDatabaseErrorsHidePasswords(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close() // nothing listening now: connection refused
	for _, tc := range []struct{ kind, dsn string }{
		{"postgres", "postgres://app:hunter2pw@" + addr + "/db?sslmode=disable"},
		{"postgres", "host=127.0.0.1 port=" + strings.Split(addr, ":")[1] + " user=app password=hunter2pw sslmode=disable"},
		{"mysql", "mysql://app:hunter2pw@" + addr + "/db"},
		{"mongodb", "mongodb://app:hunter2pw@" + addr + "/?directConnection=true"},
	} {
		r := Run(context.Background(), &store.Monitor{Type: tc.kind, Timeout: 3, Config: store.MonitorConfig{DSN: tc.dsn}})
		if r.OK {
			t.Errorf("%s: expected failure", tc.kind)
		}
		if strings.Contains(r.Msg, "hunter2pw") {
			t.Errorf("%s: password leaked into message: %s", tc.kind, r.Msg)
		}
	}
}

func TestRedactAndMySQLDSN(t *testing.T) {
	for _, tc := range []struct{ kind, in, want string }{
		{"postgres", "postgres://app:pw@db.example.com:5432/prod?sslmode=require", "postgres://app@db.example.com:5432/prod"},
		{"redis", "rediss://default:pw@cache.example.com:6380", "rediss://default@cache.example.com:6380"},
		{"mysql", "app:pw@tcp(db.example.com:3306)/shop", "mysql://app@db.example.com:3306/shop"},
		{"postgres", "host=db password=pw", "postgres"},
	} {
		if got := RedactDSN(tc.kind, tc.in); got != tc.want {
			t.Errorf("RedactDSN(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	dsn, err := MySQLDSN("mysql://app:p%40ss@db.example.com/shop")
	if err != nil || !strings.Contains(dsn, "app:p@ss@tcp(db.example.com:3306)/shop") {
		t.Fatalf("MySQLDSN = %q, %v", dsn, err)
	}
}
