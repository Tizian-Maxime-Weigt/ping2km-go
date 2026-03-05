package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const VERSION = "0.0.1"

var (
	PROGNAME string
)

// Defaults (mirror shell script)
var (
	MEDIUM        = "fiber"
	LIGHT_FACTOR  = 99.9305
	MODEM_OVERHEAD = 0.0
	COUNT         = 0
	PING_CMD_BASE = []string{"-c", "1"}
)

// Stats
var (
	CPT    = 0     // packets sent counter
	RECV   = 0     // packets received counter
	MIN    = math.Inf(1)
	MAX    = math.Inf(-1)
	SUM    = 0.0
	SUM_SQ = 0.0

	TARGET     string
	RESOLVED_IP string
	START_MS   int64
)

// Usage text (matching shell script)
func usage() {
	fmt.Printf("%s v%s - Ping with estimated distance based on the speed of light\n\n", PROGNAME, VERSION)
	fmt.Printf("Usage: %s [--fiber|--copper|--theoretical|--dialup] [-4] [-c count] <host>\n", PROGNAME)
	fmt.Printf("       %s [--help|--version]\n\n", PROGNAME)
	fmt.Println("Arguments:")
	fmt.Println("  <host>        Hostname or IP address to ping")
	fmt.Println("")
	fmt.Println("Options:")
	fmt.Println("  --fiber       Use fiber optic model: 2/3 c ~ 199,861 km/s (default)")
	fmt.Println("  --copper      Use real copper twisted pair model: ~2/3 c ~ 197,863 km/s")
	fmt.Println("  --theoretical Use speed of light in vacuum: c ~ 299,792 km/s")
	fmt.Println("  --dialup      Simulate dial-up era: subtract ~120 ms modem overhead")
	fmt.Println("  -4            Force IPv4 (passed to ping)")
	fmt.Println("  -c count      Stop after sending count packets (like ping -c)")
	fmt.Println("  --help, -h    Display this help")
	fmt.Println("  --version, -V Display version")
	fmt.Println("")
	fmt.Println("How it works:")
	fmt.Println("  Four transmission models are available:")
	fmt.Println("    fiber:       distance = RTT_ms * 99.9305 km   (2/3 c ~ 199,861 km/s)")
	fmt.Println("    copper:      distance = RTT_ms * 98.93 km     (2/3 c ~ 197,863 km/s)")
	fmt.Println("    theoretical: distance = RTT_ms * 149.896 km   (c ~ 299,792 km/s)")
	fmt.Println("    dialup:      distance = (RTT_ms - 120) * 99.9305 km")
	fmt.Println("  Since RTT is a round trip: distance = (RTT_ms / 1000 / 2) * speed")
	fmt.Println("")
	fmt.Println("  Spoiler: fiber and copper give nearly identical results (~1%")
	fmt.Println("  difference), because signal propagation speed is about 2/3 c in")
	fmt.Println("  both media. The real difference between fiber and copper is")
	fmt.Println("  bandwidth and attenuation, not signal speed.")
	fmt.Println("")
	fmt.Println("  The dialup mode subtracts the typical analog modem encoding/")
	fmt.Println("  decoding overhead (~120 ms round trip) before calculating the")
	fmt.Println("  distance. Back in the RTC days, the world was smaller.")
	fmt.Println("")
	fmt.Println("Disclaimer:")
	fmt.Println("  This estimation is intentionally naive! It does not account for")
	fmt.Println("  router/switch/firewall latency, network topology detours, buffering,")
	fmt.Println("  or application-level processing. Meant for fun, not for calibrating")
	fmt.Println("  interferometers. ;-)")
	fmt.Println("")
	fmt.Println("Compatibility:")
	fmt.Println("  Tested on Debian GNU/Linux, FreeBSD 13.x/14.x, macOS (Darwin).")
	fmt.Println("  Strictly POSIX: sed, shell arithmetic, bc. No GNU-isms, no PCRE.")
	fmt.Println("")
	fmt.Println("Examples:")
	fmt.Printf("  %s minig.deuza.bzh\n", PROGNAME)
	fmt.Printf("  %s -c 5 minig.deuza.bzh\n", PROGNAME)
	fmt.Printf("  %s --copper 1.1.1.1\n", PROGNAME)
	fmt.Printf("  %s --theoretical www.kernel.org\n", PROGNAME)
	fmt.Printf("  %s --dialup -4 -c 10 www.kernel.org\n", PROGNAME)
	fmt.Println("")
	fmt.Println("Hit Ctrl+C to stop: a statistics summary will be displayed.")
	os.Exit(0)
}

func version() {
	fmt.Printf("%s v%s\n", PROGNAME, VERSION)
	os.Exit(0)
}

func checkPingBinary() {
	_, err := exec.LookPath("ping")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: 'ping' not found in $PATH.\n")
		fmt.Fprintf(os.Stderr, "       Install it before running %s.\n", PROGNAME)
		os.Exit(1)
	}
}

// get current timestamp in ms
func getMs() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// calc_distance: distance in km (rounded) or 0 if RTT < modem overhead
func calcDistance(rttMs float64) int {
	if MODEM_OVERHEAD > 0 {
		effective := rttMs - MODEM_OVERHEAD
		if effective > 0 {
			return int(math.Round(effective * LIGHT_FACTOR))
		}
		return 0
	}
	return int(math.Round(rttMs * LIGHT_FACTOR))
}

func showStatsAndExit() {
	LOST := CPT - RECV
	lossPct := 0
	if CPT > 0 {
		lossPct = LOST * 100 / CPT
	}
	END_MS := getMs()
	ELAPSED := END_MS - START_MS

	fmt.Println()
	fmt.Printf("--- %s %s statistics ---\n", TARGET, PROGNAME)
	fmt.Printf("%d packets transmitted, %d received, %d%% packet loss, time %dms\n", CPT, RECV, lossPct, ELAPSED)

	if RECV > 0 {
		AVG := SUM / float64(RECV)
		var MDEV float64
		if RECV == 1 {
			MDEV = 0.0
		} else {
			MDEV = math.Sqrt(SUM_SQ/float64(RECV) - AVG*AVG)
		}
		AVG_KMS := calcDistance(AVG)
		MIN_KMS := calcDistance(MIN)
		MAX_KMS := calcDistance(MAX)
		// print rtt min/avg/max/mdev = ${MIN}/${AVG}/${MAX}/${MDEV} ms
		fmt.Printf("rtt min/avg/max/mdev = %.3f/%.3f/%.3f/%.3f ms\n", MIN, AVG, MAX, MDEV)
		fmt.Printf("%s min/avg/max = %d/%d/%d km\n", MEDIUM, MIN_KMS, AVG_KMS, MAX_KMS)
	}
	os.Exit(0)
}

// parse ping output to find a line containing "bytes from" and extract time, ttl and ip
func parsePingOutput(output string) (timeMs float64, ttl string, ip string, err error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	var line string
	for scanner.Scan() {
		l := scanner.Text()
		if strings.Contains(l, "bytes from") {
			line = l
			break
		}
	}
	if line == "" {
		return 0, "", "", errors.New("no reply line")
	}

	// extract time: time=([0-9.]+)
	reTime := regexp.MustCompile(`time=([0-9]+(?:\.[0-9]+)?)`)
	mt := reTime.FindStringSubmatch(line)
	if len(mt) >= 2 {
		timeMs, _ = strconv.ParseFloat(mt[1], 64)
	} else {
		return 0, "", "", errors.New("time not found")
	}

	// extract ttl=([0-9]+)
	reTTL := regexp.MustCompile(`ttl=([0-9]+)`)
	mttl := reTTL.FindStringSubmatch(line)
	if len(mttl) >= 2 {
		ttl = mttl[1]
	} else {
		ttl = "0"
	}

	// Extract IP address. Two cases:
	// 1) "from hostname (IP):"
	// 2) "from IP:"
	reIP1 := regexp.MustCompile(`from [^(]*\(([^)]+)\)`)
	mip1 := reIP1.FindStringSubmatch(line)
	if len(mip1) >= 2 {
		ip = mip1[1]
	} else {
		reIP2 := regexp.MustCompile(`from ([0-9]+\.[0-9]+\.[0-9]+\.[0-9]+)`)
		mip2 := reIP2.FindStringSubmatch(line)
		if len(mip2) >= 2 {
			ip = mip2[1]
		} else {
			ip = ""
		}
	}

	return timeMs, ttl, ip, nil
}

func main() {
	PROGNAME = os.Args[0]

	// Manual parsing to allow options and host in any order similar to shell script
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}

	// We'll parse options ourselves
	for i := 0; i < len(args); {
		a := args[i]
		switch a {
		case "--help", "-h":
			usage()
		case "--version", "-V":
			version()
		case "--fiber":
			MEDIUM = "fiber"
			LIGHT_FACTOR = 99.9305
			MODEM_OVERHEAD = 0.0
			i++
		case "--copper":
			MEDIUM = "copper"
			LIGHT_FACTOR = 98.93
			MODEM_OVERHEAD = 0.0
			i++
		case "--theoretical":
			MEDIUM = "theoretical"
			LIGHT_FACTOR = 149.896
			MODEM_OVERHEAD = 0.0
			i++
		case "--dialup":
			MEDIUM = "dialup"
			LIGHT_FACTOR = 99.9305
			MODEM_OVERHEAD = 120.0
			i++
		case "-4":
			// pass -4 to ping
			PING_CMD_BASE = append(PING_CMD_BASE, "-4")
			i++
		case "-c":
			// next arg must be count
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "ERROR: -c requires a positive integer argument.\n")
				os.Exit(1)
			}
			cntStr := args[i+1]
			n, err := strconv.Atoi(cntStr)
			if err != nil || n < 1 {
				fmt.Fprintf(os.Stderr, "ERROR: -c requires a positive integer argument.\n")
				os.Exit(1)
			}
			COUNT = n
			i += 2
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "ERROR: unknown option '%s'\n", a)
				fmt.Fprintf(os.Stderr, "Try '%s --help' for more information.\n", PROGNAME)
				os.Exit(1)
			}
			// treat as target
			if TARGET == "" {
				TARGET = a
				i++
			} else {
				// extra positional args are ignored
				i++
			}
		}
	}

	if TARGET == "" {
		fmt.Fprintf(os.Stderr, "Usage: %s [--fiber|--copper|--theoretical|--dialup] [-4] [-c count] <host>\n", PROGNAME)
		fmt.Fprintf(os.Stderr, "Try '%s --help' for more information.\n", PROGNAME)
		os.Exit(1)
	}

	// Pre-flight checks
	checkPingBinary()

	// DNS check (use net.LookupHost -- if name doesn't resolve, exit like ping(8))
	_, dnsErr := net.LookupHost(TARGET)
	if dnsErr != nil {
		fmt.Fprintf(os.Stderr, "%s: %s: Name or service not known\n", PROGNAME, TARGET)
		os.Exit(2)
	}

	// trap INT and TERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		showStatsAndExit()
	}()

	START_MS = getMs()

	// Build ping command prefix
	// ping -c 1 [maybe -4] TARGET
	for {
		// increment counter BEFORE ping to match ping(8) behavior (seq starts at 1)
		CPT++

		// run ping
		cmdArgs := append(PING_CMD_BASE, TARGET)
		cmd := exec.Command("ping", cmdArgs...)
		outBytes, _ := cmd.CombinedOutput()
		out := string(outBytes)

		// parse output
		timeMs, ttl, ip, perr := parsePingOutput(out)
		if perr == nil {
			// On first successful ping, set RESOLVED_IP and print banner like ping(8)
			if RESOLVED_IP == "" {
				RESOLVED_IP = ip
				if RESOLVED_IP != "" {
					fmt.Printf("PING %s (%s) 56 data bytes\n", TARGET, RESOLVED_IP)
				}
			}

			// Calculate distance
			kms := calcDistance(timeMs)
			distDisplay := fmt.Sprintf("%d km", kms)
			if MODEM_OVERHEAD > 0 && kms == 0 {
				distDisplay = "0 km [too close for dial-up]"
			}

			// format time like ping: the script prints the time from ping output; we'll use 3 decimals
			timeStr := fmt.Sprintf("%.3f", timeMs)

			// Print reply line
			if TARGET != RESOLVED_IP && RESOLVED_IP != "" {
				fmt.Printf("64 bytes from %s (%s): icmp_seq=%d ttl=%s time=%s ms distance=%s\n", TARGET, RESOLVED_IP, CPT, ttl, timeStr, distDisplay)
			} else {
				fmt.Printf("64 bytes from %s: icmp_seq=%d ttl=%s time=%s ms distance=%s\n", TARGET, CPT, ttl, timeStr, distDisplay)
			}

			// Update stats
			RECV++
			SUM += timeMs
			SUM_SQ += timeMs * timeMs

			if timeMs < MIN {
				MIN = timeMs
			}
			if timeMs > MAX {
				MAX = timeMs
			}

			// sleep 1s like script
			time.Sleep(1 * time.Second)
		} else {
			// lost packet / unreachable host: silent, sleep 1s like script
			time.Sleep(1 * time.Second)
		}

		// If -c specified, stop after sending COUNT packets
		if COUNT > 0 && CPT >= COUNT {
			showStatsAndExit()
		}
	}
}
