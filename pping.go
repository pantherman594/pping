package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

var (
	urls = []string{"google.com", "facebook.com"}
)

type Request struct {
	id  int
	seq int
}

type Result struct {
	id      int
	seq     int
	endTime time.Time
}

type Error struct {
	id     int
	seq    int
	ipAddr *net.IPAddr
	err    error
}

// ping sends a ping request through the provided connection to ipAddr, and
// sends the new request to the requests channel. id and seq are used to clear
// the request if it fails. Inspired by
// https://gist.github.com/lmas/c13d1c9de3b2224f9c26435eb56e6ef3
func ping(conn *icmp.PacketConn, ipAddr *net.IPAddr, id int, seq int,
	requests chan Request, errors chan Error) {
	// Create the icmp request.
	m := icmp.Message{
		Type: ipv4.ICMPTypeEcho, Code: 0,
		Body: &icmp.Echo{
			ID:   id,
			Seq:  seq,
			Data: []byte(ipAddr.String()),
		},
	}

	// Marshall it into bytes.
	b, err := m.Marshal(nil)
	if err != nil {
		errors <- Error{id, seq, ipAddr, err}
		return
	}

	// Send it.
	requests <- Request{id, seq}
	n, err := conn.WriteTo(b, ipAddr)
	if err != nil {
		errors <- Error{id, seq, ipAddr, err}
		return
	} else if n != len(b) {
		err := fmt.Errorf("got %v; want %v", n, len(b))
		errors <- Error{id, seq, ipAddr, err}
		return
	}
}

// listen opens a new PacketConn, listening for ipv4 icmp requests. When
// received, it parses the request and sends it to the results channel.
func listen(conn chan *icmp.PacketConn, results chan Result) {
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	conn <- c
	reply := make([]byte, 1500)

	// Listen for packets in an infinite loop.
	for {
		n, peer, err := c.ReadFrom(reply)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		endTime := time.Now()

		rm, err := icmp.ParseMessage(1, reply[:n])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		switch rm.Type {
		case ipv4.ICMPTypeEchoReply:
			switch pkt := rm.Body.(type) {
			case *icmp.Echo:
				// If it is a valid echo packet, send the result to the results channel.
				results <- Result{pkt.ID, pkt.Seq, endTime}
			default:
			}
		default:
			fmt.Fprintf(os.Stderr, "got %+v from %v; want echo reply\n", rm, peer)
		}
	}
}

func pinger(conn *icmp.PacketConn, ipAddrs []*net.IPAddr, requests chan Request,
	errors chan Error) {
	i := int64(0)
	l := int64(len(ipAddrs))

	// Ping each address in a round-robin fashion.
	for {
		id := int(i % l)
		seq := int(i / l)

		// Send the ping request in a new goroutine.
		go ping(conn, ipAddrs[id], id, seq, requests, errors)

		// Sleep for 1 millisecond so that the listener's buffer isn't overloaded.
		time.Sleep(time.Millisecond)

		i += 1
	}
}

func main() {
	// Set the cap of ipAddrs and matchedUrls to the length of the provided urls,
	// so that memory won't need to be reallocated later and because there cannot
	// be more than the provided urls.
	ipAddrs := make([]*net.IPAddr, 0, len(urls))
	matchedUrls := make([]string, 0, len(urls))

	// Create the channels.
	connReceiver := make(chan *icmp.PacketConn)
	requestReceiver := make(chan Request)
	resultReceiver := make(chan Result)
	errorReceiver := make(chan Error)

	// Start the listener in a goroutine, and store the PacketConn.
	go listen(connReceiver, resultReceiver)
	conn := <-connReceiver

	// For each provided url, resolve its ip address and make sure it's pingable.
	for _, url := range urls {
		// Resolve the IPv4 address.
		ip, err := net.ResolveIPAddr("ip4", url)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not get IP for %s: %v\n", url, err)
		}

		// Attempt to ping the resolved ip address.
		go ping(conn, ip, 0, 0, requestReceiver, errorReceiver)
		<-requestReceiver

		select {
		case <-resultReceiver:
			// If the ping succeeds, add it to the list of ip addresses and urls.
			ipAddrs = append(ipAddrs, ip)
			matchedUrls = append(matchedUrls, url)
		case e := <-errorReceiver:
			fmt.Fprintf(os.Stderr, "Failed to ping IP %s for %s: %v\n",
				ip.String(), url, e.err)
		}
	}

	// Precompute the number of ip addresses, because this will be referenced many
	// times later.
	ipCount := len(ipAddrs)

	if ipCount == 0 {
		log.Fatalf("Unable to find ips for any of the provided urls.\n")
	}

	// Create the requests and results with the cap ipCount.
	requests := make([]map[int]time.Time, ipCount)
	for i, l := 0, ipCount; i < l; i++ {
		requests[i] = map[int]time.Time{}
	}

	results := make([][]time.Duration, ipCount)
	for i, l := 0, ipCount; i < l; i++ {
		results[i] = []time.Duration{}
	}

	fmt.Printf("Pinging %d URLs...\n", len(matchedUrls))

	// Start the looped pinger in a separate routine so that we can handle stuff
	// in the main routine.
	go pinger(conn, ipAddrs, requestReceiver, errorReceiver)

	// Listen for requests, results, and errors.
	for {
		select {
		case req := <-requestReceiver:
			requests[req.id][req.seq] = time.Now()
		case res := <-resultReceiver:
			if res.id >= ipCount {
				fmt.Fprintln(os.Stderr, "Received invalid id.")
				continue
			}

			start, ok := requests[res.id][res.seq]
			if !ok {
				fmt.Fprintln(os.Stderr,
					"Response received without a corresponding request.")
				continue
			}
			delete(requests[res.id], res.seq)

			// Calculate the total duration, log it, and store it in results.
			dur := res.endTime.Sub(start)
			durMs := float64(dur.Nanoseconds()) / float64(time.Millisecond)

			fmt.Printf("Pinged %s in %0.2fms.\n", ipAddrs[res.id], durMs)
			results[res.id] = append(results[res.id], dur)
		case e := <-errorReceiver:
			if e.id < ipCount {
				delete(requests[e.id], e.seq)
			}

			fmt.Fprintf(os.Stderr, "Failed to ping IP %s: %v\n",
				e.ipAddr.String(), e.err)
		}
	}
}
