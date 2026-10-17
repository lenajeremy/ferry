// Ferry shares files between this computer and the phones and computers on the same network,
// through a web page: photos, videos and anything else, either way. This is its command-line
// program; desktop/ is the same in a window.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"ferry/qr"
	"ferry/server"
)

func main() {
	cfg := server.DefaultConfig()
	flag.StringVar(&cfg.Dir, "dir", cfg.Dir, "the shared folder")
	flag.IntVar(&cfg.Port, "port", cfg.Port, "port for the page")
	flag.IntVar(&cfg.TLSPort, "tls-port", cfg.TLSPort, "port for the secure page (0 turns it off)")
	flag.StringVar(&cfg.DataDir, "data", cfg.DataDir, "Ferry's own data (code, certificate, thumbnails)")
	flag.BoolVar(&cfg.Bonjour, "bonjour", cfg.Bonjour, "announce Ferry on the network for the phone apps")
	flag.StringVar(&cfg.Name, "name", "", "the name the phones see (default: this computer's name)")
	open := flag.Bool("open", true, "open the page in the browser on start")
	flag.Parse()

	cfg.Ready = func(s *server.Server) {
		enableTerminalColours()
		banner(s, cfg.Port)
		if *open {
			server.Open(fmt.Sprintf("http://localhost:%d", cfg.Port))
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, cfg); err != nil {
		if errors.Is(err, server.ErrPortInUse) {
			log.Fatalf("%v: is Ferry already running?", err)
		}
		log.Fatal(err)
	}
	fmt.Println("\n  Ferry stopped.")
}

func banner(s *server.Server, port int) {
	byName, byIP := s.PhoneURLs()
	code := s.Code()
	strip := func(u string) string { return strings.Split(u, "/?")[0] }
	fmt.Printf("\n  Ferry is running.\n\n")
	fmt.Printf("  Folder     %s\n", s.Folder())
	fmt.Printf("  Here       http://localhost:%d\n", port)
	if byName != "" {
		fmt.Printf("  Phone      scan the code below, or open %s\n", strip(byName))
		fmt.Printf("             and type the code %s %s\n", code[:4], code[4:])
		if byIP != "" && byIP != byName {
			fmt.Printf("             (if that address doesn't open: %s)\n", strip(byIP))
		}
	} else {
		fmt.Printf("  Phone      this computer isn't on a network\n")
	}
	if c, err := qr.Encode(byName); err == nil && byName != "" {
		// Black on white whatever the terminal's colours, so any camera reads it.
		fmt.Println()
		for _, line := range strings.Split(strings.TrimRight(c.Terminal(false), "\n"), "\n") {
			fmt.Printf("  \x1b[30;107m%s\x1b[0m\n", strings.TrimPrefix(line, "  "))
		}
	}
	fmt.Printf("\n  Leave this running while you share. Ctrl-C stops Ferry.\n\n")
}
