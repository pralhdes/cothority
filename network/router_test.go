package network

import (
	"testing"
	"time"

	"github.com/dedis/cothority/log"
	"github.com/stretchr/testify/assert"
)

func NewTestRouterTCP(port int) *Router {
	h := NewTestTCPHost(port)
	return NewRouter(h.id, h)
}

func NewTestRouterLocal(port int) *Router {
	h := NewTestLocalHost(port)
	return NewRouter(h.id, h)
}

type routerFactory func(port int) *Router

// Test if router fits the interface such as calling Run(), then Stop(),
// should return
func TestRouterTCP(t *testing.T) {
	testRouter(t, NewTestRouterTCP)
}
func TestRouterLocal(t *testing.T) {
	testRouter(t, NewTestRouterLocal)
}

func testRouter(t *testing.T, fac routerFactory) {
	h := fac(2004)
	var stop = make(chan bool)
	go func() {
		stop <- true
		h.Start()
		stop <- true
	}()
	<-stop
	// Time needed so the listener is up. Equivalent to "connecting ourself" as
	// we had before.
	time.Sleep(250 * time.Millisecond)
	h.Stop()
	select {
	case <-stop:
		return
	case <-time.After(500 * time.Millisecond):
		t.Fatal("TcpHost should have returned from Run() by now")
	}
}

// Test the automatic connection upon request
func TestRouterAutoConnectionTCP(t *testing.T) {
	testRouterAutoConnection(t, NewTestRouterTCP)
}
func TestRouterAutoConnectionLocal(t *testing.T) {
	testRouterAutoConnection(t, NewTestRouterLocal)
}

func testRouterAutoConnection(t *testing.T, fac routerFactory) {
	h1 := fac(2007)
	h2 := fac(2008)
	go h2.Start()

	proc := newSimpleMessageProc(t)
	h2.RegisterProcessor(proc, SimpleMessageType)
	defer func() {
		h1.Stop()
		h2.Stop()
		time.Sleep(250 * time.Millisecond)
	}()

	err := h1.Send(h2.id, &SimpleMessage{12})
	if err != nil {
		t.Fatal("Couldn't send message:", err)
	}

	// Receive the message
	msg := <-proc.relay
	if msg.I != 12 {
		t.Fatal("Simple message got distorted")
	}
}

// Test connection of multiple Hosts and sending messages back and forth
// also tests for the counterIO interface that it works well
func TestRouterMessaging(t *testing.T) {
	h1 := NewTestRouterTCP(2009)
	h2 := NewTestRouterTCP(2010)
	go h1.Start()
	go h2.Start()

	defer func() {
		h1.Stop()
		h2.Stop()
		time.Sleep(250 * time.Millisecond)
	}()

	proc := &simpleMessageProc{t, make(chan SimpleMessage)}
	h1.RegisterProcessor(proc, SimpleMessageType)
	h2.RegisterProcessor(proc, SimpleMessageType)

	msgSimple := &SimpleMessage{3}
	err := h1.Send(h2.id, msgSimple)
	if err != nil {
		t.Fatal("Couldn't send from h2 -> h1:", err)
	}
	decoded := <-proc.relay
	if decoded.I != 3 {
		t.Fatal("Received message from h2 -> h1 is wrong")
	}

	// make sure the connection is registered in host1 (because it's launched in
	// a go routine). Since we try to avoid random timeout, let's send a msg
	// from host2 -> host1.
	assert.Nil(t, h2.Send(h1.id, msgSimple))
	decoded = <-proc.relay
	assert.Equal(t, 3, decoded.I)

	written := h1.Tx()
	read := h2.Rx()
	if written == 0 || read == 0 || written != read {
		t.Logf("Tx = %d, Rx = %d", written, read)
		t.Logf("h1.Tx() %d vs h2.Rx() %d", h1.Tx(), h2.Rx())
		t.Fatal("Something is wrong with Host.CounterIO")
	}
}

// Test sending data back and forth using the sendSDAData
func TestRouterSendMsgDuplexTCP(t *testing.T) {
	testRouterSendMsgDuplex(t, NewTestRouterTCP)
}

func TestRouterSendMsgDuplexLocal(t *testing.T) {
	testRouterSendMsgDuplex(t, NewTestRouterLocal)
}
func testRouterSendMsgDuplex(t *testing.T, fac routerFactory) {
	h1 := fac(2011)
	h2 := fac(2012)
	go h1.Start()
	go h2.Start()

	defer func() {
		h1.Stop()
		h2.Stop()
		time.Sleep(250 * time.Millisecond)
	}()

	proc := &simpleMessageProc{t, make(chan SimpleMessage)}
	h1.RegisterProcessor(proc, SimpleMessageType)
	h2.RegisterProcessor(proc, SimpleMessageType)

	msgSimple := &SimpleMessage{5}
	err := h1.Send(h2.id, msgSimple)
	if err != nil {
		t.Fatal("Couldn't send message from h1 to h2", err)
	}
	msg := <-proc.relay
	log.Lvl2("Received msg h1 -> h2", msg)

	err = h2.Send(h1.id, msgSimple)
	if err != nil {
		t.Fatal("Couldn't send message from h2 to h1", err)
	}
	msg = <-proc.relay
	log.Lvl2("Received msg h2 -> h1", msg)
}

func TestRouterExchange(t *testing.T) {

	entity1 := NewTestServerIdentity("tcp://localhost:7878")
	entity2 := NewTestServerIdentity("tcp://localhost:8787")

	router1 := NewRouter(entity1, NewTCPHost(entity1))
	router2 := NewRouter(entity2, NewTCPHost(entity2))

	done := make(chan bool)
	go func() {
		done <- true
		router1.Start()
		done <- true
	}()
	<-done
	// try correctly
	c, err := NewTCPConn(entity1.Address.NetworkAddress())
	if err != nil {
		t.Fatal("Couldn't connect to host1:", err)
	}
	if err := router2.negotiateOpen(entity1, c); err != nil {
		t.Fatal("Wrong negotiation")
	}
	c.Close()

	// try giving wrong id
	c, err = NewTCPConn(entity1.Address.NetworkAddress())
	if err != nil {
		t.Fatal("Couldn't connect to host1:", err)
	}
	if err := router2.negotiateOpen(entity2, c); err == nil {
		t.Fatal("negotiation should have aborted")
	}
	c.Close()

	log.Lvl4("Closing connections")
	if err := router2.Stop(); err != nil {
		t.Fatal("Couldn't close host", router2)
	}
	if err := router1.Stop(); err != nil {
		t.Fatal("Couldn't close host", router1)
	}
	<-done
}