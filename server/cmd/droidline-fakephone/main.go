// Command droidline-fakephone is a simulated phone for trying Droidline, and for
// SDK tests in CI, without a device. It runs a small demo app: a launcher, a
// login screen and a main screen.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KnifeLemon/Droidline/server/internal/fakeagent"
)

func main() {
	state := flag.String("state", "fakephone.json", "file that keeps this simulated phone's key and pairing")
	qr := flag.String("qr", "", "pair with a droidline://pair URI from `droidline pair`")
	model := flag.String("model", "Droidline Fake", "model name the phone reports")
	notify := flag.Duration("notify", 0, "post a demo notification from com.kakao.talk this often, e.g. 20s")
	verbose := flag.Bool("v", false, "verbose log")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	agent, err := load(*state, *model, log)
	if err != nil {
		fail(err)
	}
	save := func() {
		b, _ := json.MarshalIndent(agent.Save(), "", "  ")
		if err := os.WriteFile(*state, b, 0o600); err != nil {
			log.Error("saving state", "err", err)
		}
	}

	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		agent.Stop()
		os.Exit(0)
	}()
	if *notify > 0 {
		go func() {
			for i := 1; ; i++ {
				time.Sleep(*notify)
				agent.PostNotification("com.kakao.talk", "Demo", fmt.Sprintf("Your code is %04d", 1000+i*37%9000))
			}
		}()
	}

	if agent.Paired() == nil {
		agent.OnSAS = func(code string) {
			fmt.Printf("\nPairing code: %s\nOn the PC run: droidline pair %s\n\n", code, code)
		}
		var perr error
		go func() {
			for agent.Paired() == nil {
				time.Sleep(200 * time.Millisecond)
			}
			save()
			fmt.Println("Paired. Device id:", agent.DeviceID)
		}()
		if *qr != "" {
			perr = agent.PairQR(*qr)
		} else {
			addr, pub, name, err := fakeagent.Discover(3 * time.Second)
			if err != nil {
				fail(err)
			}
			fmt.Printf("Found %s at %s\n", name, addr)
			perr = agent.PairCode(addr, pub)
		}
		if agent.Paired() == nil {
			fail(fmt.Errorf("pairing failed: %v", perr))
		}
	}
	fmt.Println("Connected as", agent.DeviceID, "- press Ctrl+C to stop")
	fail(agent.Run())
}

func load(path, model string, log *slog.Logger) (*fakeagent.Agent, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fakeagent.New("", model, log)
	}
	if err != nil {
		return nil, err
	}
	var s fakeagent.Saved
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return fakeagent.Restore(s, log)
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
