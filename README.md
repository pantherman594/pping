pping
=====

_Parallel ping written in Go_

Build
-----

`go build`

Usage
-----

`pping [-o output_file] [-p max_procs] {destinations...}`

_**pping** must be run with administrator privileges, in order to send raw ICMP
packets._

Description
-----------

**pping** uses the ICMP protocol to concurrently send requests to the
destinations provided. These can be 1 or more urls or IPv4 addresses. It
records the round trip times for each request and can output it to a specified
output file.

**ping** works only with IPv4 at this time.

Options
-------

#### -h

Show help.

#### -o _output_file_

Write the results to _output_file_ if provided, in CSV format.

#### -p _max_procs_

Sets the value of `runtime.GOMAXPROCS` to _max_procs_. If _max_procs_ is set to
-1 **pping** will print the default value for `runtime.GOMAXPROCS` and quit.

#### _destinations..._

A space separated list of at least 1 url or IPv4 address to ping.

How it works
------------

**pping** pings one or more addresses concurrently, through the usage of
goroutines.

The `main` routine first runs a one-time setup. It attempts to resolve each url
provided to its IPv4 url, and tries to send a ping to that address. If all of
this succeeds, it adds the address and url to a list.

The `main` routine also starts two goroutines: `listener` and `pinger`. The
`listener` routine constantly listences for ICMP packets, to receive ping
responses After a ping is received, `listener` writes the information, including
the receive time, to the `results` channel. The `pinger` routine continuously
executes new `ping` goroutines, hitting each provided address in a round-robin
fashion. As a new request is sent, `ping` also writes to the `requests` channel
to store the starting time.

The code for `ping` was inspired by https://gist.github.com/lmas/c13d1c9de3b2224f9c26435eb56e6ef3,
but with a single shared listener for every ping request in the `listener`
routine. It first creates the ICMP message, storing the ID (which is the index
of the address in the list of addresses to ping) and the sequence number (which
increments every time all the addresses have been pinged). It marshalls this
into a slice of bytes, which is sent through the packet connection created by
the listener.

After starting the goroutines, the `main` routine reads from the `requests` and
`results` channels, as well as the `errors` channel to handle any errors that
may have occurred during the `ping` routine. It stores each new request in a
map for that address, with the start time. When a result is received, it looks
up the starting time in that map, computes the duration, and stores it in a new
jagged 2D array named `results`, and clears the map entry.
