package collect

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hook/internal/proto"
)

// systemd status, read-only, via `systemctl show` and `systemctl list-units`.
// Unit names come from the hub, so they're validated and passed after "--":
// they can only ever be unit names, never options or shell syntax.

var UnitName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9@._:\\-]{0,127}$`)

func systemctl(ctx context.Context, args ...string) (string, error) {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, args...).Output()
	return string(out), err
}

// units reports the watched units. bootTime turns monotonic stamps into wall time.
func units(ctx context.Context, names []string, bootTime time.Time) []proto.Unit {
	var valid []string
	for _, n := range names {
		if UnitName.MatchString(n) {
			if !strings.Contains(n, ".") {
				n += ".service"
			}
			valid = append(valid, n)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	args := append([]string{"show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,NRestarts,StateChangeTimestampMonotonic", "--"}, valid...)
	out, err := systemctl(ctx, args...)
	if err != nil && out == "" {
		return nil
	}
	return parseUnits(out, bootTime)
}

// parseUnits reads `systemctl show` output: key=value blocks separated by blank lines.
func parseUnits(out string, bootTime time.Time) []proto.Unit {
	var res []proto.Unit
	for _, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		kv := map[string]string{}
		sc := bufio.NewScanner(strings.NewReader(block))
		for sc.Scan() {
			if k, v, ok := strings.Cut(sc.Text(), "="); ok {
				kv[k] = v
			}
		}
		if kv["Id"] == "" {
			continue
		}
		u := proto.Unit{Name: kv["Id"], Active: kv["ActiveState"], Sub: kv["SubState"]}
		if kv["LoadState"] == "not-found" {
			u.Active, u.Sub = "not-found", "not installed"
		}
		u.Restarts, _ = strconv.Atoi(kv["NRestarts"])
		if mono, err := strconv.ParseInt(kv["StateChangeTimestampMonotonic"], 10, 64); err == nil && mono > 0 {
			u.Since = bootTime.Add(time.Duration(mono) * time.Microsecond).UnixMilli()
		}
		res = append(res, u)
	}
	return res
}

func failedUnits(ctx context.Context) []string {
	out, err := systemctl(ctx, "list-units", "--state=failed", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			names = append(names, f[0])
		}
	}
	return names
}
