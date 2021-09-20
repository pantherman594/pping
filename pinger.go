package main

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// ping sends a ping request through the provided connection to ipAddr, and
// sends the new request to the requests channel. id and seq are used to clear
// the request if it fails. Inspired by
// https://gist.github.com/lmas/c13d1c9de3b2224f9c26435eb56e6ef3
func Ping(conn *icmp.PacketConn, ipAddr *net.IPAddr, id int, seq int,
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

func Pinger(conn *icmp.PacketConn, ipAddrs []*net.IPAddr, requests chan Request,
	errors chan Error, quit chan struct{}) {
	i := int64(0)
	l := int64(len(ipAddrs))

	// Ping each address in a round-robin fashion.
	for {
		id := int(i % l)
		seq := int(i / l)

		// Send the ping request in a new goroutine.
		go Ping(conn, ipAddrs[id], id, seq, requests, errors)

		// Sleep for 1 millisecond so that the listener's buffer isn't overloaded.
		time.Sleep(time.Millisecond)

		i += 1

		select {
		case <-quit:
			return
		default:
		}
	}
}
