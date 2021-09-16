package main

import (
	"fmt"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

var (
	urls = []string{"google.com"}
	matchedUrls []string
	ipAddrs []*net.IPAddr
	requests []map[int]time.Time
	results [][]time.Duration
	mu sync.Mutex
)

type Request struct {
	id int
	seq int
}

type Result struct {
	id int
	seq int
	endTime time.Time
}

type Error struct {
	id int
	seq int
	ipAddr *net.IPAddr
	err error
}

func ping(conn *icmp.PacketConn, ipAddr *net.IPAddr, id int, seq int, requests chan Request, errors chan Error) {
	// fmt.Printf("Pinging %s.\n", ipAddr.String())
	m := icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{
			ID: id,
			Seq: seq,
			Data: []byte(ipAddr.String()),
		},
	}

	b, err := m.Marshal(nil)
	if err != nil {
		errors <- Error{ id, seq, ipAddr, err }
		return
	}

	// Send it
	requests <- Request{ id, seq  }
	n, err := conn.WriteTo(b, ipAddr)
	if err != nil {
		errors <- Error{ id, seq, ipAddr, err }
		return
	} else if n != len(b) {
		err := fmt.Errorf("got %v; want %v", n, len(b))
		errors <- Error{ id, seq, ipAddr, err }
		return
	}
}

func sendResult(n int, peer net.Addr, reply []byte, results chan Result) {
	rm, err := icmp.ParseMessage(1, reply[:n])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	switch rm.Type {
	case ipv4.ICMPTypeEchoReply:
		switch pkt := rm.Body.(type) {
		case *icmp.Echo:
			endTime := time.Now()
			results <- Result{pkt.ID, pkt.Seq, endTime}
		default:
		}
	default:
		fmt.Fprintf(os.Stderr, "got %+v from %v; want echo reply\n", rm, peer)
	}
}

func listen(conn chan *icmp.PacketConn, results chan Result) {
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		log.Fatal(err)
	}
	conn <- c
	defer c.Close()

	for {
		reply := make([]byte, 1500)
		n, peer, err := c.ReadFrom(reply)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}

		sendResult(n, peer, reply, results)
	}
}

func pinger(conn *icmp.PacketConn, requests chan Request, errors chan Error) {
	i := int64(0)
	l := int64(len(ipAddrs))

	// Ping each address in a round-robin fashion
	for {
		id := int(i % l)
		seq := int(i / l)
		go ping(conn, ipAddrs[id], id, seq, requests, errors)
		i++
	}
}

// https://gist.github.com/lmas/c13d1c9de3b2224f9c26435eb56e6ef3
func main() {
	fmt.Println("Hello!")

	ipAddrs = make([]*net.IPAddr, 0, len(urls))
	matchedUrls = make([]string, 0, len(urls))

	connReceiver := make(chan *icmp.PacketConn)
	requestReceiver := make(chan Request)
	resultReceiver := make(chan Result)
	errorReceiver := make(chan Error)

	go listen(connReceiver, resultReceiver)
	conn := <-connReceiver

	// For each provided url, resolve its ip address and make sure it's pingable
	for _, url := range urls {
		ip, err := net.ResolveIPAddr("ip4", url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not get IP for %s: %v\n", url, err)
		}

		go ping(conn, ip, 0, 0, requestReceiver, errorReceiver)
		<-requestReceiver

		select {
		case <-resultReceiver:
			ipAddrs = append(ipAddrs, ip)
			matchedUrls = append(matchedUrls, url)
		case e := <- errorReceiver:
			fmt.Fprintf(os.Stderr, "Failed to ping IP %s for %s: %v\n", ip.String(), url, e.err)
		}
	}

	requests = make([]map[int]time.Time, len(ipAddrs))
	for i, l := 0, len(ipAddrs); i < l; i++ {
		requests[i] = map[int]time.Time{}
	}

	results = make([][]time.Duration, len(ipAddrs))
	for i, l := 0, len(ipAddrs); i < l; i++ {
		results[i] = []time.Duration{}
	}

	if len(ipAddrs) == 0 {
		log.Fatalf("Unable to find ips for any of the provided urls.\n")
	}

	fmt.Printf("Pinging %d URLs...\n", len(matchedUrls))

	// Start the looped pinger in a separate routine so that we can handle stuff in the main routine.
	go pinger(conn, requestReceiver, errorReceiver)

	for {
		select {
		case req := <-requestReceiver:
			requests[req.id][req.seq] = time.Now()
		case res := <-resultReceiver:
			start, ok := requests[res.id][res.seq]
			if !ok {
				log.Fatal("Response received without a corresponding request.")
			}
			delete(requests[res.id], res.seq)

			dur := res.endTime.Sub(start)
			fmt.Printf("Pinged %s in %vms.\n", ipAddrs[res.id], float64(dur.Nanoseconds()) / 1000000.0)
			results[res.id] = append(results[res.id], dur)
		case e := <-errorReceiver:
			delete(requests[e.id], e.seq)
			fmt.Fprintf(os.Stderr, "Failed to ping IP %s: %v\n", e.ipAddr.String(), e.err)
		}
	}
}