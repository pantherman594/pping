package main

import (
	"time"
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
	id  int
	seq int
	err error
}
