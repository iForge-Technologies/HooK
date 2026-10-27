package checks

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"hook/internal/store"
)

// Database checks open a fresh connection, run the cheapest possible query,
// and close it. Use a read-only user: the check never writes.

var DBTypes = []string{"postgres", "mysql", "redis", "mongodb"}

// ErrUnverified refuses to send a database password over a connection whose
// server hasn't proven who it is: anyone answering for the host could read it.
var ErrUnverified = errors.New("the connection to this database isn't verified, so a server posing as it could learn the password. " +
	"Use sslmode=verify-full (PostgreSQL) or tls=true (MySQL), or turn on \"Allow unverified TLS\" to accept that risk")

// isLocal reports loopback addresses and unix sockets, where nobody can sit in
// between.
func isLocal(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasPrefix(host, "/") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

// PostgresConfig parses a PostgreSQL connection string and applies the hub's
// rule for the password. Unless allowUnverified is set, every non-local host
// must use TLS with a verified certificate (sslmode=verify-full or
// verify-ca). When it is set, password auth that would hand the password over
// in the clear is still refused, unless the string names require_auth itself.
func PostgresConfig(dsn string, allowUnverified bool) (*pgx.ConnConfig, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid connection string: " + dbErr(err, dsn))
	}
	verified := func(host string, t *tls.Config) bool {
		return isLocal(host) || (t != nil && (!t.InsecureSkipVerify || t.VerifyPeerCertificate != nil))
	}
	ok := verified(cfg.Host, cfg.TLSConfig)
	for _, f := range cfg.Fallbacks {
		ok = ok && verified(f.Host, f.TLSConfig)
	}
	if ok {
		return cfg, nil
	}
	if !allowUnverified {
		return nil, ErrUnverified
	}
	if cfg.RequireAuth == "" {
		cfg.RequireAuth = "scram-sha-256"
	}
	return cfg, nil
}

func checkPostgres(ctx context.Context, m *store.Monitor) Result {
	cfg, err := PostgresConfig(m.Config.DSN, m.Config.IgnoreTLS)
	if err != nil {
		return Result{Msg: err.Error()}
	}
	start := time.Now()
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return Result{Latency: time.Since(start), Msg: dbErr(err, m.Config.DSN)}
	}
	defer conn.Close(context.Background())
	var version string
	if err := conn.QueryRow(ctx, "SELECT current_setting('server_version')").Scan(&version); err != nil {
		return Result{Latency: time.Since(start), Msg: dbErr(err, m.Config.DSN)}
	}
	msg := "PostgreSQL"
	if f := strings.Fields(version); len(f) > 0 { // the server's word: may be empty
		msg += " " + f[0]
	}
	return Result{OK: true, Latency: time.Since(start), Msg: msg}
}

func checkMySQL(ctx context.Context, m *store.Monitor) Result {
	dsn, err := MySQLDSN(m.Config.DSN, m.Config.IgnoreTLS)
	if err != nil {
		return Result{Msg: err.Error()}
	}
	start := time.Now()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return Result{Msg: dbErr(err, m.Config.DSN)}
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var version string
	if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return Result{Latency: time.Since(start), Msg: dbErr(err, m.Config.DSN)}
	}
	return Result{OK: true, Latency: time.Since(start), Msg: "MySQL " + version}
}

// MySQLDSN accepts the driver's native form (user:pass@tcp(host:3306)/db) or a
// mysql://user:pass@host:3306/db URL. Non-local servers get verified TLS
// (tls=true) unless the string asks for something else; anything weaker needs
// allowUnverified (see PostgresConfig).
func MySQLDSN(s string, allowUnverified bool) (string, error) {
	cfg, err := mysqlConfig(s)
	if err != nil {
		return "", errors.New("connection string should look like mysql://user:pass@host:3306/db")
	}
	host, _, _ := net.SplitHostPort(cfg.Addr)
	if cfg.Net == "tcp" && !isLocal(host) {
		switch cfg.TLSConfig {
		case "":
			cfg.TLSConfig = "true"
		case "true":
		default: // false, skip-verify, preferred
			if !allowUnverified {
				return "", ErrUnverified
			}
		}
	}
	return cfg.FormatDSN(), nil
}

func mysqlConfig(s string) (*mysql.Config, error) {
	if !strings.HasPrefix(s, "mysql://") {
		return mysql.ParseDSN(s)
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return nil, errors.New("bad URL")
	}
	cfg := mysql.NewConfig()
	cfg.User = u.User.Username()
	cfg.Passwd, _ = u.User.Password()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	if u.Port() == "" {
		cfg.Addr = net.JoinHostPort(u.Hostname(), "3306")
	}
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	if v := u.Query().Get("tls"); v != "" {
		cfg.TLSConfig = v
	}
	return cfg, nil
}

func checkMongo(ctx context.Context, m *store.Monitor) Result {
	start := time.Now()
	timeout := time.Duration(m.Timeout) * time.Second
	client, err := mongo.Connect(options.Client().ApplyURI(m.Config.DSN).SetServerSelectionTimeout(timeout).SetConnectTimeout(timeout).SetMaxPoolSize(1))
	if err != nil {
		return Result{Msg: dbErr(err, m.Config.DSN)}
	}
	defer client.Disconnect(context.Background())
	if err := client.Ping(ctx, readpref.PrimaryPreferred()); err != nil {
		return Result{Latency: time.Since(start), Msg: dbErr(err, m.Config.DSN)}
	}
	return Result{OK: true, Latency: time.Since(start), Msg: "MongoDB reachable"}
}

// checkRedis speaks just enough RESP for AUTH, PING and INFO server.
func checkRedis(ctx context.Context, m *store.Monitor) Result {
	u, err := url.Parse(m.Config.DSN)
	if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Host == "" {
		return Result{Msg: "connection string should look like redis://:password@host:6379/0"}
	}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "6379")
	}
	start := time.Now()
	var conn net.Conn
	d := &net.Dialer{}
	if u.Scheme == "rediss" {
		conn, err = (&tls.Dialer{NetDialer: d, Config: &tls.Config{ServerName: u.Hostname()}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return Result{Latency: time.Since(start), Msg: errMsg(err)}
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	r := bufio.NewReader(conn)
	cmd := func(args ...string) (string, error) {
		var b strings.Builder
		fmt.Fprintf(&b, "*%d\r\n", len(args))
		for _, a := range args {
			fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
		}
		if _, err := conn.Write([]byte(b.String())); err != nil {
			return "", err
		}
		return readRESP(r)
	}
	if pass, ok := u.User.Password(); ok {
		args := []string{"AUTH", pass}
		if name := u.User.Username(); name != "" && name != "default" {
			args = []string{"AUTH", name, pass}
		}
		if _, err := cmd(args...); err != nil {
			return Result{Latency: time.Since(start), Msg: "AUTH failed: " + err.Error()}
		}
	}
	if db := strings.TrimPrefix(u.Path, "/"); db != "" && db != "0" {
		if _, err := cmd("SELECT", db); err != nil {
			return Result{Latency: time.Since(start), Msg: "SELECT failed: " + err.Error()}
		}
	}
	pong, err := cmd("PING")
	lat := time.Since(start)
	if err != nil {
		return Result{Latency: lat, Msg: errMsg(err)}
	}
	if pong != "PONG" {
		return Result{Latency: lat, Msg: "unexpected reply to PING: " + pong}
	}
	msg := "Redis PONG"
	if info, err := cmd("INFO", "server"); err == nil {
		if m := redisVersion.FindStringSubmatch(info); m != nil {
			msg = "Redis " + m[1]
		}
	}
	return Result{OK: true, Latency: lat, Msg: msg}
}

var redisVersion = regexp.MustCompile(`redis_version:(\S+)`)

// maxRESPLine caps one reply line; a real PING or INFO header is tiny.
const maxRESPLine = 64 << 10

// readRESP reads one simple, error, integer or bulk reply.
func readRESP(r *bufio.Reader) (string, error) {
	line, err := readLine(r, maxRESPLine)
	if err != nil {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return "", errors.New("empty reply")
	}
	switch line[0] {
	case '+', ':':
		return line[1:], nil
	case '-':
		return "", errors.New(line[1:])
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil || n < 0 {
			return "", nil
		}
		if n > 1<<20 {
			return "", errors.New("reply too large")
		}
		buf := make([]byte, n+2)
		if _, err := readFull(r, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	}
	return "", fmt.Errorf("unexpected reply %q", line)
}

// readLine reads up to and including '\n', failing once the line passes max
// bytes instead of buffering whatever the server sends.
func readLine(r *bufio.Reader, max int) (string, error) {
	var b []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(b)+len(chunk) > max {
			return "", errors.New("reply too large")
		}
		b = append(b, chunk...)
		if err == nil {
			return string(b), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", err
		}
	}
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	n := 0
	for n < len(buf) {
		k, err := r.Read(buf[n:])
		n += k
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// dbErr shortens driver errors and makes sure the password never leaks into
// a heartbeat message or an alert.
var pwKV = regexp.MustCompile(`(?i)(password\s*=\s*)\S+`)

func dbErr(err error, dsn string) string {
	msg := pwKV.ReplaceAllString(errMsg(err), "${1}***")
	if u, perr := url.Parse(dsn); perr == nil {
		if p, ok := u.User.Password(); ok && p != "" {
			msg = strings.ReplaceAll(msg, p, "***")
		}
	}
	if i := strings.Index(dsn, "@"); i > 0 {
		if j := strings.Index(dsn[:i], ":"); j >= 0 && j+1 < i {
			msg = strings.ReplaceAll(msg, dsn[j+1:i], "***")
		}
	}
	return msg
}

// RedactDSN returns a display form without credentials, used as the target.
func RedactDSN(kind, dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Host != "" {
		host := u.Host
		path := strings.TrimPrefix(u.Path, "/")
		user := ""
		if u.User != nil && u.User.Username() != "" {
			user = u.User.Username() + "@"
		}
		if path != "" {
			return fmt.Sprintf("%s://%s%s/%s", u.Scheme, user, host, path)
		}
		return fmt.Sprintf("%s://%s%s", u.Scheme, user, host)
	}
	if kind == "mysql" {
		if cfg, err := mysql.ParseDSN(dsn); err == nil {
			return fmt.Sprintf("mysql://%s@%s/%s", cfg.User, cfg.Addr, cfg.DBName)
		}
	}
	return kind
}
