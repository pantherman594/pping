package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// listener opens a new PacketConn, listening for ipv4 icmp requests. When
// received, it parses the request and sends it to the results channel.
func Listener(conn chan *icmp.PacketConn, results chan Result,
	quit chan struct{}) {
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

		select {
		case <-quit:
			return
		default:
		}
	}
}
