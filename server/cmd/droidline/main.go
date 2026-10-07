// Command droidline is the PC server, the CLI for every phone command, and
// the MCP adapter, in one binary.
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/client"
	"github.com/KnifeLemon/Droidline/server/internal/hub"
	"github.com/KnifeLemon/Droidline/server/internal/mcp"
	"github.com/KnifeLemon/Droidline/server/internal/store"
	"github.com/KnifeLemon/Droidline/spec"
)

type globals struct {
	addr    string
	token   string
	device  string
	home    string
	json    bool
	verbose bool
}

func main() {
	g, args := parseGlobals(os.Args[1:])
	if len(args) == 0 {
		usage(os.Stdout)
		return
	}
	var err error
	switch args[0] {
	case "serve":
		err = cmdServe(g)
	case "pair":
		err = cmdPair(g, args[1:])
	case "devices":
		err = cmdDevices(g)
	case "token":
		err = cmdToken(g, args[1:])
	case "relay":
		err = cmdRelay(g, args[1:])
	case "proxy":
		if len(args) > 1 && (args[1] == "add" || args[1] == "list" || args[1] == "remove") {
			err = cmdProxyProfiles(g, args[1:])
		} else {
			err = cmdDevice(g, args)
		}
	case "webhook":
		err = cmdWebhook(g, args[1:])
	case "mcp":
		err = mcp.Run(mcp.Options{Addr: g.addr, Token: g.token, Device: g.device, Home: g.home})
	case "doctor":
		err = cmdDoctor(g)
	case "version", "--version", "-v":
		fmt.Println("droidline", hub.Version)
	case "help", "--help", "-h":
		err = cmdHelp(args[1:])
	default:
		err = cmdDevice(g, args)
	}
	if err != nil {
		var ue usageErr
		fmt.Fprintln(os.Stderr, err)
		if errors.As(err, &ue) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

type usageErr struct{ msg string }

func (u usageErr) Error() string { return u.msg }

func parseGlobals(in []string) (globals, []string) {
	g := globals{
		addr:   envOr("DROIDLINE_ADDR", "127.0.0.1:"+envOr("DROIDLINE_PORT", "8780")),
		token:  os.Getenv("DROIDLINE_TOKEN"),
		device: os.Getenv("DROIDLINE_DEVICE"),
		home:   store.DefaultDir(),
	}
	if h := os.Getenv("DROIDLINE_HOST"); h != "" {
		g.addr = h + ":" + envOr("DROIDLINE_PORT", "8780")
	}
	var rest []string
	for i := 0; i < len(in); i++ {
		a := in[i]
		val := func() string {
			if k, v, ok := strings.Cut(a, "="); ok && strings.HasPrefix(k, "--") {
				return v
			}
			if i+1 < len(in) {
				i++
				return in[i]
			}
			return ""
		}
		switch name, _, _ := strings.Cut(a, "="); name {
		case "--addr":
			g.addr = val()
		case "--token":
			g.token = val()
		case "--device", "-d":
			g.device = val()
		case "--home":
			g.home = val()
		case "--json":
			g.json = true
		case "--verbose":
			g.verbose = true
		default:
			rest = append(rest, a)
		}
	}
	return g, rest
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func dial(g globals) (*client.Client, error) { return client.Dial(g.addr, g.token) }

func call(g globals, req map[string]any) (map[string]any, error) {
	c, err := dial(g)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	return c.Call(req)
}

// failure turns an ok:false response into an error printed as the server's message.
func failure(res map[string]any) error {
	if ok, _ := res["ok"].(bool); ok {
		return nil
	}
	if m, _ := res["msg"].(string); m != "" {
		return fmt.Errorf("%s: %s", res["error"], m)
	}
	return fmt.Errorf("%v", res["error"])
}

func cmdDevice(g globals, args []string) error {
	sp := spec.MustLoad()
	name := args[0]
	cmd, aliasBy := sp.Command(name)
	if cmd == nil {
		msg := fmt.Sprintf("unknown command %q. Run droidline help for the list.", name)
		if s := sp.Similar(name, 3); len(s) > 0 {
			msg += " Did you mean: " + strings.Join(s, ", ") + "?"
		}
		return usageErr{msg}
	}
	if cmd.Scope == "client" {
		return streamNotifications(g, args[1:])
	}
	req, localPath, err := buildRequest(sp, cmd, aliasBy, name, args[1:])
	if err != nil {
		return usageErr{fmt.Sprintf("%v\nusage: %s", err, signature(cmd, aliasBy, name))}
	}
	if g.device != "" && cmd.Scope == "device" || (cmd.Name == "wait_notification" && g.device != "") {
		req["device"] = g.device
	}
	res, err := call(g, req)
	if err != nil {
		return err
	}
	if g.json {
		b, _ := json.Marshal(res)
		fmt.Println(string(b))
		return failure(res)
	}
	if err := failure(res); err != nil {
		return err
	}
	return printResult(cmd, res, localPath)
}

func buildRequest(sp *spec.Spec, cmd *spec.Command, aliasBy, name string, args []string) (map[string]any, string, error) {
	req := map[string]any{"cmd": name}
	var positional []string
	localPath := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") || len(a) < 3 || isNumber(a) {
			positional = append(positional, a)
			continue
		}
		key, val, hasVal := strings.Cut(a[2:], "=")
		key = strings.ReplaceAll(key, "-", "_")
		switch key {
		case "wait":
			req["wait"] = !hasVal || val == "true"
			continue
		case "offline_wait":
			if !hasVal && i+1 < len(args) {
				i++
				val = args[i]
			}
			f, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return nil, "", fmt.Errorf("--offline-wait needs seconds")
			}
			req["offline_wait"] = f
			continue
		}
		p := cmd.Param(key)
		if p == nil {
			return nil, "", fmt.Errorf("%s has no option --%s", cmd.Name, key)
		}
		if !hasVal {
			if p.Type == "bool" && (i+1 >= len(args) || strings.HasPrefix(args[i+1], "--")) {
				val = "true"
			} else if i+1 < len(args) {
				i++
				val = args[i]
			}
		}
		if p.Name == "path" {
			localPath = val
			continue
		}
		v, err := sp.ParseArg(p, val)
		if err != nil {
			return nil, "", err
		}
		req[p.Name] = v
	}

	var order []spec.Param
	for _, p := range cmd.Params {
		if aliasBy != "" && p.Name == "by" || p.Type == "function" {
			continue
		}
		if p.ClientOnly && p.Name != "path" {
			continue
		}
		order = append(order, p)
	}
	for i := 0; i < len(positional); i++ {
		if i >= len(order) {
			return nil, "", fmt.Errorf("too many arguments")
		}
		p := order[i]
		raw := positional[i]
		if p.Type == "list<selector_pair>" && !strings.HasPrefix(raw, "[") {
			// which text=Log in id=main_tab: every remaining by=value is a candidate.
			var pairs []any
			for ; i < len(positional) && strings.Contains(positional[i], "="); i++ {
				by, v, _ := strings.Cut(positional[i], "=")
				pairs = append(pairs, []any{by, v})
			}
			i--
			req[p.Name] = pairs
			continue
		}
		if p.Name == "path" {
			localPath = raw
			continue
		}
		v, err := sp.ParseArg(&p, raw)
		if err != nil {
			return nil, "", err
		}
		req[p.Name] = v
	}
	if cmd.SDKSave == "image" && localPath != "" {
		if _, set := req["format"]; !set {
			ext := strings.ToLower(filepath.Ext(localPath))
			if ext == ".png" {
				req["format"] = "png"
			} else {
				req["format"] = "jpeg"
			}
		}
	}
	return req, localPath, nil
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

func printResult(cmd *spec.Command, res map[string]any, localPath string) error {
	switch cmd.SDKSave {
	case "image":
		data, _ := res["data"].(string)
		img, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return err
		}
		if localPath == "" {
			ext := ".jpg"
			if res["format"] == "png" {
				ext = ".png"
			}
			localPath = "screen-" + time.Now().Format("20060102-150405") + ext
		}
		if err := os.WriteFile(localPath, img, 0o644); err != nil {
			return err
		}
		fmt.Printf("%s (%vx%v)\n", localPath, res["width"], res["height"])
		return nil
	case "json":
		delete(res, "ok")
		b, _ := json.MarshalIndent(res, "", "  ")
		if localPath != "" {
			if err := os.WriteFile(localPath, b, 0o644); err != nil {
				return err
			}
			fmt.Println(localPath)
			return nil
		}
		fmt.Println(string(b))
		return nil
	}
	if res["accepted"] == true {
		fmt.Println("accepted; the phone will finish after its connection comes back (add --wait to wait for it)")
		return nil
	}
	switch cmd.Returns.Kind {
	case "value":
		v := res["value"]
		switch t := v.(type) {
		case string:
			fmt.Println(t)
		case bool, float64:
			fmt.Println(t)
		default:
			b, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(b))
		}
	case "fields":
		delete(res, "ok")
		b, _ := json.Marshal(res)
		fmt.Println(string(b))
	default:
		fmt.Println("ok")
	}
	return nil
}

func streamNotifications(g globals, args []string) error {
	pkg, contains := "", ""
	for i := 0; i < len(args); i++ {
		k, v, has := strings.Cut(strings.TrimPrefix(args[i], "--"), "=")
		if !has && i+1 < len(args) {
			i++
			v = args[i]
		}
		switch k {
		case "package":
			pkg = v
		case "textContains", "text_contains", "text-contains":
			contains = v
		default:
			return usageErr{"usage: droidline on_notification [--package P] [--textContains TEXT]"}
		}
	}
	c, err := dial(g)
	if err != nil {
		return err
	}
	defer c.Close()
	req := map[string]any{"cmd": "subscribe", "events": []any{"notification"}}
	if g.device != "" {
		req["device"] = g.device
	}
	res, err := c.Call(req)
	if err != nil {
		return err
	}
	if err := failure(res); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "waiting for notifications; press Ctrl+C to stop")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	for {
		select {
		case ev := <-c.Events:
			if pkg != "" && ev["package"] != pkg {
				continue
			}
			t, _ := ev["title"].(string)
			x, _ := ev["text"].(string)
			if contains != "" && !strings.Contains(t, contains) && !strings.Contains(x, contains) {
				continue
			}
			b, _ := json.Marshal(ev)
			fmt.Println(string(b))
		case <-stop:
			return nil
		}
	}
}

func cmdDevices(g globals) error {
	res, err := call(g, map[string]any{"cmd": "devices"})
	if err != nil {
		return err
	}
	if g.json {
		b, _ := json.Marshal(res)
		fmt.Println(string(b))
		return failure(res)
	}
	if err := failure(res); err != nil {
		return err
	}
	list, _ := res["value"].([]any)
	if len(list) == 0 {
		fmt.Println("No phones paired yet. Run: droidline pair")
	} else {
		rows := [][]string{{"NAME", "ID", "MODEL", "ANDROID", "STATE", "ROUTE", "MISSING"}}
		for _, it := range list {
			d, _ := it.(map[string]any)
			state := "offline"
			if d["online"] == true {
				state = "online"
			}
			var missing []string
			if r, ok := d["ready"].(map[string]any); ok {
				for k, v := range r {
					if v != true {
						missing = append(missing, k)
					}
				}
				sort.Strings(missing)
			}
			rows = append(rows, []string{str(d["name"]), str(d["id"]), str(d["model"]), str(d["release"]), state, str(d["route"]), strings.Join(missing, ",")})
		}
		printTable(rows)
	}
	if pend, _ := res["pending"].([]any); len(pend) > 0 {
		fmt.Printf("\n%d phone(s) waiting to pair. Type the code each one shows: droidline pair <code>\n", len(pend))
	}
	return nil
}

func str(v any) string {
	if v == nil {
		return "-"
	}
	s := fmt.Sprint(v)
	if s == "" {
		return "-"
	}
	return s
}

func printTable(rows [][]string) {
	widths := make([]int, len(rows[0]))
	for _, r := range rows {
		for i, c := range r {
			widths[i] = max(widths[i], len([]rune(c)))
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			b.WriteString(c)
			if i < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(c))+2))
			}
		}
		fmt.Println(strings.TrimRight(b.String(), " "))
	}
}
