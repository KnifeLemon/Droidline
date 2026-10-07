package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/client"
	"github.com/KnifeLemon/Droidline/server/internal/dlcrypto"
	"github.com/KnifeLemon/Droidline/server/internal/hub"
	"github.com/KnifeLemon/Droidline/server/internal/server"
	"github.com/KnifeLemon/Droidline/server/internal/store"
)

func cmdServe(g globals) error {
	st, err := store.Open(g.home)
	if err != nil {
		return err
	}
	level := slog.LevelInfo
	if g.verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	srv, err := server.Start(st, log)
	if err != nil {
		return err
	}
	defer srv.Close()
	fmt.Fprintf(os.Stderr, "Droidline %s on %s\n", hub.Version, st.Config.Name)
	fmt.Fprintf(os.Stderr, "  phones connect to   :%d (TCP, TLS and WebSocket)\n", srv.AgentPort)
	fmt.Fprintf(os.Stderr, "  your code connects  %s\n", st.Config.Client.Listen)
	fmt.Fprintf(os.Stderr, "  settings            %s\n", st.Dir)
	if len(st.Devices()) == 0 {
		fmt.Fprintln(os.Stderr, "\nNo phone paired yet. In another terminal run: droidline pair")
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	fmt.Fprintln(os.Stderr, "stopping")
	return nil
}

func cmdPair(g globals, args []string) error {
	name := ""
	var rest []string
	for i := 0; i < len(args); i++ {
		if k, v, ok := strings.Cut(args[i], "="); ok && k == "--name" {
			name = v
		} else if args[i] == "--name" && i+1 < len(args) {
			i++
			name = args[i]
		} else {
			rest = append(rest, args[i])
		}
	}
	if len(rest) == 1 {
		req := map[string]any{"cmd": "pair", "code": rest[0]}
		if name != "" {
			req["name"] = name
		}
		res, err := call(g, req)
		if err != nil {
			return err
		}
		if err := failure(res); err != nil {
			return err
		}
		d, _ := res["value"].(map[string]any)
		fmt.Printf("Paired %s (%s). Try: droidline --device %s info\n", str(d["name"]), str(d["model"]), str(d["name"]))
		return nil
	}
	if len(rest) > 1 {
		return usageErr{"usage: droidline pair [code] [--name NAME]"}
	}
	req := map[string]any{"cmd": "pair_qr"}
	if name != "" {
		req["name"] = name
	}
	res, err := call(g, req)
	if err != nil {
		return err
	}
	if err := failure(res); err != nil {
		return err
	}
	uri, _ := res["uri"].(string)
	if err := printQR(os.Stdout, uri); err != nil {
		fmt.Println(uri)
	}
	exp := time.Unix(int64(res["expires"].(float64)), 0)
	fmt.Printf("\nScan this with the Droidline app (valid until %s, one phone).\n", exp.Format("15:04"))
	fmt.Println("No camera? On the same Wi-Fi, tap \"Pair on this Wi-Fi\" in the app and run:")
	fmt.Println("  droidline pair <the 6-digit code the phone shows>")
	if u, err := url.Parse(uri); err == nil {
		if a := u.Query().Get("a"); a == "" {
			fmt.Println("\nNote: this PC has no LAN address to offer. Phones must reach it through [remote] or [relay] settings.")
		}
	}
	return nil
}

func cmdToken(g globals, args []string) error {
	st, err := store.Open(g.home)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return usageErr{"usage: droidline token create [label] | token list | token revoke <id>"}
	}
	switch args[0] {
	case "create":
		label := strings.Join(args[1:], " ")
		id := dlcrypto.RandomID(6)
		tok := "dlc_" + dlcrypto.RandomToken() + dlcrypto.RandomToken()
		if err := st.AddClientToken(id, tok, label); err != nil {
			return err
		}
		fmt.Println(tok)
		fmt.Fprintf(os.Stderr, "Token %s created. It is shown once; Droidline stores only its hash.\n", id)
		fmt.Fprintln(os.Stderr, "Use it with DROIDLINE_TOKEN, --token, or connect(token=...).")
	case "list":
		rows := [][]string{{"ID", "LABEL", "CREATED"}}
		for _, t := range st.ClientTokens() {
			rows = append(rows, []string{t.ID, str(t.Label), time.Unix(t.Created, 0).Format("2006-01-02 15:04")})
		}
		printTable(rows)
	case "revoke":
		if len(args) != 2 {
			return usageErr{"usage: droidline token revoke <id>"}
		}
		found, err := st.RemoveClientToken(args[1])
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("no token with id %s", args[1])
		}
		fmt.Println("revoked")
	default:
		return usageErr{"usage: droidline token create [label] | token list | token revoke <id>"}
	}
	return nil
}

func cmdRelay(g globals, args []string) error {
	st, err := store.Open(g.home)
	if err != nil {
		return err
	}
	switch {
	case len(args) == 3 && args[0] == "set":
		u, err := url.Parse(args[1])
		if err != nil || (u.Scheme != "https" && u.Scheme != "wss" && u.Scheme != "http" && u.Scheme != "ws") || u.Host == "" {
			return usageErr{"relay url must look like https://relay.example.workers.dev"}
		}
		if u.Scheme == "http" || u.Scheme == "ws" {
			fmt.Fprintln(os.Stderr, "warning: plain http relay; use https outside a test network")
		}
		if err := st.SetSecret("relay_token", args[2]); err != nil {
			return err
		}
		if err := st.SetRelayURL(args[1]); err != nil {
			return err
		}
		fmt.Println("Relay saved. Restart droidline serve; paired phones receive the relay address on their next connection.")
	case len(args) == 1 && args[0] == "off":
		st.SetSecret("relay_token", "")
		if err := st.SetRelayURL(""); err != nil {
			return err
		}
		fmt.Println("Relay removed. Restart droidline serve.")
	case len(args) == 0 || args[0] == "status":
		if st.Config.Relay.URL == "" {
			fmt.Println("No relay configured.")
		} else {
			fmt.Printf("Relay: %s (token %s)\n", st.Config.Relay.URL, map[bool]string{true: "set", false: "missing"}[st.Secret("relay_token") != ""])
		}
	default:
		return usageErr{"usage: droidline relay set <url> <token> | relay off | relay status"}
	}
	return nil
}

func cmdProxyProfiles(g globals, args []string) error {
	st, err := store.Open(g.home)
	if err != nil {
		return err
	}
	switch {
	case args[0] == "add" && len(args) == 3:
		u, err := url.Parse(args[2])
		if err != nil || (u.Scheme != "socks5" && u.Scheme != "http") || u.Port() == "" {
			return usageErr{"proxy url must look like socks5://user:pass@host:port or http://host:port"}
		}
		if err := st.SetSecret("proxy:"+args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("Saved. Use proxy(\"@%s\", app=\"com.android.chrome\")\n", args[1])
	case args[0] == "list":
		for _, k := range st.SecretKeys("proxy:") {
			u, _ := url.Parse(st.Secret(k))
			if u != nil && u.User != nil {
				u.User = url.User(u.User.Username())
			}
			fmt.Printf("@%s  %s\n", strings.TrimPrefix(k, "proxy:"), u)
		}
	case args[0] == "remove" && len(args) == 2:
		return st.SetSecret("proxy:"+args[1], "")
	default:
		return usageErr{"usage: droidline proxy add <name> <url> | proxy list | proxy remove <name>"}
	}
	return nil
}

func cmdWebhook(g globals, args []string) error {
	st, err := store.Open(g.home)
	if err != nil {
		return err
	}
	if len(args) != 1 || args[0] != "secret" {
		return usageErr{"usage: droidline webhook secret"}
	}
	s := st.Secret("webhook_secret")
	if s == "" {
		s = "whsec_" + dlcrypto.RandomToken()
		if err := st.SetSecret("webhook_secret", s); err != nil {
			return err
		}
	}
	fmt.Println(s)
	fmt.Fprintln(os.Stderr, "Verify X-Droidline-Signature = sha256=hex(HMAC-SHA256(secret, X-Droidline-Timestamp + \".\" + body)).")
	return nil
}

func cmdDoctor(g globals) error {
	st, err := store.Open(g.home)
	if err != nil {
		return err
	}
	check := func(ok bool, label, hint string) {
		mark := "ok  "
		if !ok {
			mark = "FAIL"
		}
		fmt.Printf("[%s] %s", mark, label)
		if !ok && hint != "" {
			fmt.Printf("\n       %s", hint)
		}
		fmt.Println()
	}
	fmt.Printf("settings: %s\nsecrets:  %s\n\n", st.Dir, store.SecretBackend())
	c, err := client.Dial(g.addr, g.token)
	running := err == nil
	check(running, "server answers on "+g.addr, "start it with: droidline serve")
	if running {
		res, _ := c.Call(map[string]any{"cmd": "server_info"})
		c.Close()
		if res != nil {
			fmt.Printf("       version %v, agent port %v, language %v\n", res["version"], res["agent_port"], res["lang"])
		}
	} else if errors.Is(err, client.ErrNotRunning) {
		ln, lerr := net.Listen("tcp", st.Config.Agent.Listen)
		check(lerr == nil, "agent port "+st.Config.Agent.Listen+" is free", "another program uses it; change [agent] listen in config.toml")
		if ln != nil {
			ln.Close()
		}
	}
	devs := st.Devices()
	check(len(devs) > 0, fmt.Sprintf("%d paired phone(s)", len(devs)), "pair one with: droidline pair")
	addrs := hub.LANAddresses(8779)
	check(len(addrs) > 0, "this PC has a private network address", "phones on Wi-Fi need the PC on the same network; otherwise use a relay")
	return nil
}
