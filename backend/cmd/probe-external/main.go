// Command probe-external is an operator-only probe. Tests use local fake upstreams.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

type options struct {
	provider                string
	n                       int
	match                   time.Duration
	sendImage, proxy, state string
}

func parseOptions(args []string, home string) (options, error) {
	var o options
	f := flag.NewFlagSet("probe-external", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&o.provider, "provider", "", "chat2019 or wanfeng")
	f.IntVar(&o.n, "n", 1, "number of identities")
	f.DurationVar(&o.match, "match", 30*time.Second, "match/observation duration after connection")
	f.StringVar(&o.sendImage, "send-image", "", "optional image path")
	f.StringVar(&o.proxy, "proxy", "", "optional wanfeng HTTP/HTTPS/SOCKS5 proxy")
	f.StringVar(&o.state, "state", filepath.Join(home, ".anonchat-probe", "state.json"), "private binding state file")
	if err := f.Parse(args); err != nil {
		return o, errors.New("参数错误；用法: probe-external -provider chat2019|wanfeng -n 1 -match 30s [-send-image FILE] [-proxy URL] [-state FILE]")
	}
	if f.NArg() != 0 || (o.provider != "chat2019" && o.provider != "wanfeng") || o.n < 1 || o.match <= 0 || o.state == "" {
		return o, errors.New("需要合法 provider、正数 n/match 和非空 state，不接受位置参数")
	}
	if o.proxy != "" {
		u, err := url.Parse(o.proxy)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return o, errors.New("proxy 必须为合法 http/https/socks5 URL")
		}
		if o.provider != "wanfeng" {
			return o, errors.New("proxy 仅用于晚风；chat2019 按服务端规则直连")
		}
	}
	return o, nil
}

func main() {
	home, err := os.UserHomeDir()
	if err == nil {
		var o options
		o, err = parseOptions(os.Args[1:], home)
		if err == nil {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			err = run(ctx, o, loadEndpoints(os.Getenv), standardGuard, os.Stdout)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
