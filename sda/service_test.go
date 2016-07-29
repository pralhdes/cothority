package sda

import (
	"testing"
	"time"

	"sync"

	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/network"
	"github.com/stretchr/testify/assert"
)

func TestServiceRegistration(t *testing.T) {
	var name = "dummy"
	RegisterNewService(name, func(c *Context, path string) Service {
		return &DummyService{}
	})

	names := ServiceFactory.RegisteredServicesName()
	var found bool
	for _, n := range names {
		if n == name {
			found = true
		}
	}
	if !found {
		t.Fatal("Name not found !?")
	}
	DeleteNewService(name)
	names = ServiceFactory.RegisteredServicesName()
	for _, n := range names {
		if n == name {
			t.Fatal("Dummy should not be found!")
		}
	}
}

type DummyProtocol struct {
	*TreeNodeInstance
	link   chan bool
	config DummyConfig
}

type DummyConfig struct {
	A    int
	Send bool
}

type DummyMsg struct {
	A int
}

var dummyMsgType network.MessageTypeID

func init() {
	dummyMsgType = network.RegisterMessageType(DummyMsg{})
}

func NewDummyProtocol(tni *TreeNodeInstance, conf DummyConfig, link chan bool) *DummyProtocol {
	return &DummyProtocol{tni, link, conf}
}

func (dm *DummyProtocol) Start() error {
	dm.link <- true
	if dm.config.Send {
		if err := dm.SendTo(dm.TreeNode(), &DummyMsg{}); err != nil {
			log.Error(err)
		}
		// also send to the children if any
		if !dm.IsLeaf() {
			if err := dm.SendTo(dm.Children()[0], &DummyMsg{}); err != nil {
				log.Error(err)
			}
		}
	}
	return nil
}

func (dm *DummyProtocol) ProcessProtocolMsg(msg *ProtocolMsg) {
	dm.link <- true
}

// legcy reasons
func (dm *DummyProtocol) Dispatch() error {
	return nil
}

type DummyService struct {
	c        *Context
	path     string
	link     chan bool
	fakeTree *Tree
	firstTni *TreeNodeInstance
	Config   DummyConfig
}

func (ds *DummyService) ProcessClientRequest(e *network.ServerIdentity, r *ClientRequest) {
	msgT, _, err := network.UnmarshalRegisteredType(r.Data, network.DefaultConstructors(network.Suite))
	if err != nil || msgT != dummyMsgType {
		ds.link <- false
		return
	}
	if ds.firstTni == nil {
		ds.firstTni = ds.c.NewTreeNodeInstance(ds.fakeTree, ds.fakeTree.Root, "DummyService")
	}

	dp := NewDummyProtocol(ds.firstTni, ds.Config, ds.link)

	if err := ds.c.RegisterProtocolInstance(dp); err != nil {
		ds.link <- false
		return
	}
	dp.Start()
}

func (ds *DummyService) NewProtocol(tn *TreeNodeInstance, conf *GenericConfig) (ProtocolInstance, error) {
	dp := NewDummyProtocol(tn, DummyConfig{}, ds.link)
	return dp, nil
}

func (ds *DummyService) Process(packet *network.Packet) {
	if packet.MsgType != dummyMsgType {
		ds.link <- false
		return
	}
	dms := packet.Msg.(DummyMsg)
	if dms.A != 10 {
		ds.link <- false
		return
	}
	ds.link <- true
}

func TestServiceNew(t *testing.T) {
	ds := &DummyService{
		link: make(chan bool),
	}
	RegisterNewService("DummyService", func(c *Context, path string) Service {
		ds.c = c
		ds.path = path
		ds.link <- true
		return ds
	})
	go func() {
		h := NewTestHost(2000)
		h.Close()
	}()

	waitOrFatal(ds.link, t)
}

func TestServiceChannels(t *testing.T) {
	sc1 := &ServiceChannels{}
	sc2 := &ServiceChannels{}
	var count int
	// Needed because of TestServiceNew which listens on a channel. Strange that
	// it worked before.
	DeleteNewService("DummyService")
	RegisterNewService("ChannelsService", func(c *Context, path string) Service {
		var sc *ServiceChannels
		if count == 0 {
			sc = sc1
		} else {
			sc = sc2
		}
		count++
		sc.ctx = c
		sc.path = path
		return sc
	})
	h1, h2 := TwoTestHosts()
	defer h1.Close()
	defer h2.Close()
	// Add tree + entitylist
	el := NewRoster([]*network.ServerIdentity{h1.ServerIdentity, h2.ServerIdentity})
	tree := el.GenerateBinaryTree()
	sc1.tree = *tree
	h1.AddRoster(el)
	h1.AddTree(tree)
	sc1.ProcessClientRequest(nil, nil)
	select {
	case msg := <-Incoming:
		if msg.I != 12 {
			t.Fatal("Child should receive 12")
		}
	case <-time.After(time.Millisecond * 100):
		t.Fatal("Timeout")
	}
}

func TestServiceProcessRequest(t *testing.T) {
	ds := &DummyService{
		link: make(chan bool),
	}
	RegisterNewService("DummyService", func(c *Context, path string) Service {
		ds.c = c
		ds.path = path
		return ds
	})
	host := NewTestHost(2000)
	log.Lvl1("Host created and listening")
	defer host.Close()
	// Send a request to the service
	re := &ClientRequest{
		Service: ServiceFactory.ServiceID("DummyService"),
		Data:    []byte("a"),
	}
	// fake a client
	h2 := NewTestHost(2010)
	defer h2.Close()
	log.Lvl1("Sending request to service...")
	if err := h2.SendRaw(host.ServerIdentity, re); err != nil {
		t.Fatal(err)
	}
	// wait for the link
	select {
	case v := <-ds.link:
		if v {
			t.Fatal("was expecting false !")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Too late")
	}
}

// Test if a request that makes the service create a new protocol works
func TestServiceRequestNewProtocol(t *testing.T) {
	ds := &DummyService{
		link: make(chan bool),
	}
	RegisterNewService("DummyService", func(c *Context, path string) Service {
		ds.c = c
		ds.path = path
		return ds
	})
	host := NewTestHost(2000)
	host.Listen()
	log.Lvl1("Host created and listening")
	defer host.Close()
	// create the entityList and tree
	el := NewRoster([]*network.ServerIdentity{host.ServerIdentity})
	tree := el.GenerateBinaryTree()
	// give it to the service
	ds.fakeTree = tree

	// Send a request to the service
	b, err := network.MarshalRegisteredType(&DummyMsg{10})
	log.ErrFatal(err)
	re := &ClientRequest{
		Service: ServiceFactory.ServiceID("DummyService"),
		Data:    b,
	}
	// fake a client
	h2 := NewTestHost(2010)
	defer h2.Close()
	log.Lvl1("Sending request to service...")
	if err := h2.SendRaw(host.ServerIdentity, re); err != nil {
		t.Fatal(err)
	}
	// wait for the link from the
	waitOrFatalValue(ds.link, true, t)

	// Now RESEND the value so we instantiate using the SAME TREENODE
	log.Lvl1("Sending request AGAIN to service...")
	if err := h2.SendRaw(host.ServerIdentity, re); err != nil {
		t.Fatal(err)
	}
	// wait for the link from the
	// NOW expect false
	waitOrFatalValue(ds.link, false, t)
}

func TestServiceProtocolProcessMessage(t *testing.T) {
	ds := &DummyService{
		link: make(chan bool),
	}
	var count int
	RegisterNewService("DummyService", func(c *Context, path string) Service {
		if count == 0 {
			count++
			// the client does not need a Service
			return &DummyService{link: make(chan bool)}
		}
		ds.c = c
		ds.path = path
		ds.Config = DummyConfig{
			Send: true,
		}
		return ds
	})
	// fake a client
	h2 := NewTestHost(2010)
	defer h2.Close()

	host := NewTestHost(2000)
	log.Lvl1("Host created and listening")
	defer host.Close()
	// create the entityList and tree
	el := NewRoster([]*network.ServerIdentity{host.ServerIdentity})
	tree := el.GenerateBinaryTree()
	// give it to the service
	ds.fakeTree = tree

	// Send a request to the service
	b, err := network.MarshalRegisteredType(&DummyMsg{10})
	log.ErrFatal(err)
	re := &ClientRequest{
		Service: ServiceFactory.ServiceID("DummyService"),
		Data:    b,
	}
	log.Lvl1("Sending request to service...")
	if err := h2.SendRaw(host.ServerIdentity, re); err != nil {
		t.Fatal(err)
	}
	// wait for the link from the protocol
	waitOrFatalValue(ds.link, true, t)

	// now wait for the same link as the protocol should have sent a message to
	// himself !
	waitOrFatalValue(ds.link, true, t)
}

// test for calling the NewProtocol method on a remote Service
func TestServiceNewProtocol(t *testing.T) {
	ds1 := &DummyService{
		link: make(chan bool),
		Config: DummyConfig{
			Send: true,
		},
	}
	ds2 := &DummyService{
		link: make(chan bool),
	}
	var count int
	RegisterNewService("DummyService", func(c *Context, path string) Service {
		var localDs *DummyService
		switch count {
		case 2:
			// the client does not need a Service
			return &DummyService{link: make(chan bool)}
		case 1: // children
			localDs = ds2
		case 0: // root
			localDs = ds1
		}
		localDs.c = c
		localDs.path = path

		count++
		return localDs
	})
	host := NewTestHost(2000)
	host.ListenAndBind()
	log.Lvl1("Host created and listening")
	defer host.Close()

	host2 := NewTestHost(2002)
	host2.ListenAndBind()
	defer host2.Close()
	// create the entityList and tree
	el := NewRoster([]*network.ServerIdentity{host.ServerIdentity, host2.ServerIdentity})
	tree := el.GenerateBinaryTree()
	// give it to the service
	ds1.fakeTree = tree

	// Send a request to the service
	b, err := network.MarshalRegisteredType(&DummyMsg{10})
	log.ErrFatal(err)
	re := &ClientRequest{
		Service: ServiceFactory.ServiceID("DummyService"),
		Data:    b,
	}
	// fake a client
	client := NewTestHost(2010)
	defer client.Close()
	log.Lvl1("Sending request to service...")
	if err := client.SendRaw(host.ServerIdentity, re); err != nil {
		t.Fatal(err)
	}
	// wait for the link from the protocol that Starts
	waitOrFatalValue(ds1.link, true, t)
	// now wait for the same link as the protocol should have sent a message to
	// himself !
	waitOrFatalValue(ds1.link, true, t)
	// now wait for the SECOND LINK on the SECOND HOST that the SECOND SERVICE
	// should have started (ds2) in ProcessRequest
	waitOrFatalValue(ds2.link, true, t)
}

func TestServiceProcessServiceMessage(t *testing.T) {
	ds1 := &DummyService{
		link: make(chan bool),
	}
	ds2 := &DummyService{
		link: make(chan bool),
	}
	var count int
	RegisterNewService("DummyService", func(c *Context, path string) Service {
		var s *DummyService
		if count == 0 {
			s = ds1
		} else {
			s = ds2
		}
		s.c = c
		s.path = path
		c.RegisterProcessor(s, dummyMsgType)
		return s
	})
	// create two hosts
	h2 := NewTestHost(2010)
	defer h2.Close()
	h1 := NewTestHost(2000)
	h1.ListenAndBind()
	defer h1.Close()
	log.Lvl1("Host created and listening")
	// create request
	log.Lvl1("Sending request to service...")
	assert.Nil(t, h2.SendRaw(h1.ServerIdentity, &DummyMsg{10}))

	// wait for the link from the Service on host 1
	waitOrFatalValue(ds1.link, true, t)
}

type clientProc struct {
	t     *testing.T
	relay chan simpleResponse
}

func newClientProc(t *testing.T) *clientProc {
	return &clientProc{
		relay: make(chan simpleResponse),
	}
}

func (c *clientProc) Process(p *network.Packet) {
	if p.MsgType != simpleResponseType {
		c.t.Fatal("Message type not simpleResponseType")
	}
	c.relay <- p.Msg.(simpleResponse)
}

func TestServiceBackForthProtocol(t *testing.T) {
	local := NewLocalTest()
	defer local.CloseAll()

	// register service
	RegisterNewService("BackForth", func(c *Context, path string) Service {
		return &simpleService{
			ctx: c,
		}
	})
	// create hosts
	hosts, el, _ := local.GenTestTree(4, true, true, false)

	// create client
	client := NewTestHost(5000)
	defer client.Close()
	proc := newClientProc(t)
	client.RegisterProcessor(proc, simpleResponseType)

	// create request
	r := &simpleRequest{
		ServerIdentities: el,
		Val:              10,
	}
	buff, err := network.MarshalRegisteredType(r)
	assert.Nil(t, err)

	req := &ClientRequest{
		Service: ServiceFactory.ServiceID("BackForth"),
		Data:    buff,
	}
	assert.Nil(t, client.SendRaw(hosts[0].ServerIdentity, req))
	select {
	case msg := <-proc.relay:
		assert.Equal(t, msg.Val, 10)
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Not received any response from host")
	}
}

func TestClient_Send(t *testing.T) {
	local := NewLocalTest()
	defer local.CloseAll()

	// register service
	RegisterNewService("BackForth", func(c *Context, path string) Service {
		return &simpleService{
			ctx: c,
		}
	})
	// create hosts
	hosts, el, _ := local.GenTestTree(4, true, true, false)
	client := NewLocalClient("BackForth")

	r := &simpleRequest{
		ServerIdentities: el,
		Val:              10,
	}
	nm, err := client.Send(hosts[0].ServerIdentity, r)
	log.ErrFatal(err)

	assert.Equal(t, nm.MsgType, simpleResponseType)
	resp := nm.Msg.(simpleResponse)
	assert.Equal(t, resp.Val, 10)
}

func TestClient_LocalSend(t *testing.T) {
	local := NewLocalTest()
	defer local.CloseAll()

	// register service
	RegisterNewService("BackForth", func(c *Context, path string) Service {
		return &simpleService{
			ctx: c,
		}
	})
	// create hosts
	hosts, el, _ := local.GenTestTree(4, true, true, false)
	client := NewLocalClient("BackForth")

	r := &simpleRequest{
		ServerIdentities: el,
		Val:              10,
	}
	nm, err := client.Send(hosts[0].ServerIdentity, r)
	log.ErrFatal(err)

	assert.Equal(t, nm.MsgType, simpleResponseType)
	resp := nm.Msg.(simpleResponse)
	assert.Equal(t, resp.Val, 10)
}

func TestClient_Parallel(t *testing.T) {
	nbrNodes := 2
	nbrParallel := 2
	local := NewLocalTest()
	defer local.CloseAll()

	// register service
	RegisterNewService("BackForth", func(c *Context, path string) Service {
		return &simpleService{
			ctx: c,
		}
	})
	// create hosts
	hosts, el, _ := local.GenTree(nbrNodes, true, true, false)

	wg := sync.WaitGroup{}
	wg.Add(nbrParallel)
	for i := 0; i < nbrParallel; i++ {
		go func(i int) {
			log.Lvl1("Starting message", i)
			r := &simpleRequest{
				ServerIdentities: el,
				Val:              10 * i,
			}
			client := NewClient("BackForth")
			nm, err := client.Send(hosts[0].ServerIdentity, r)
			log.ErrFatal(err)

			assert.Equal(t, nm.MsgType, simpleResponseType)
			resp := nm.Msg.(simpleResponse)
			assert.Equal(t, resp.Val, 10*i)
			log.Lvl1("Done with message", i)
			wg.Done()
		}(i)
	}
	wg.Wait()
}

// BackForthProtocolForth & Back are messages that go down and up the tree.
// => BackForthProtocol protocol / message
type SimpleMessageForth struct {
	Val int
}

type SimpleMessageBack struct {
	Val int
}

var simpleMessageForthType = network.RegisterMessageType(SimpleMessageForth{})
var simpleMessageBackType = network.RegisterMessageType(SimpleMessageBack{})

type BackForthProtocol struct {
	*TreeNodeInstance
	Val       int
	counter   int
	forthChan chan struct {
		*TreeNode
		SimpleMessageForth
	}
	backChan chan struct {
		*TreeNode
		SimpleMessageBack
	}
	handler func(val int)
}

func newBackForthProtocolRoot(tn *TreeNodeInstance, val int, handler func(int)) (ProtocolInstance, error) {
	s, err := newBackForthProtocol(tn)
	s.Val = val
	s.handler = handler
	return s, err
}

func newBackForthProtocol(tn *TreeNodeInstance) (*BackForthProtocol, error) {
	s := &BackForthProtocol{
		TreeNodeInstance: tn,
	}
	err := s.RegisterChannel(&s.forthChan)
	if err != nil {
		return nil, err
	}
	err = s.RegisterChannel(&s.backChan)
	go s.dispatch()
	return s, nil
}

func (sp *BackForthProtocol) Start() error {
	// send down to children
	msg := &SimpleMessageForth{
		Val: sp.Val,
	}
	for _, ch := range sp.Children() {
		if err := sp.SendTo(ch, msg); err != nil {
			return err
		}
	}
	return nil
}

func (sp *BackForthProtocol) dispatch() {
	for {
		select {
		// dispatch the first msg down
		case m := <-sp.forthChan:
			msg := &m.SimpleMessageForth
			for _, ch := range sp.Children() {
				sp.SendTo(ch, msg)
			}
			if sp.IsLeaf() {
				if err := sp.SendTo(sp.Parent(), &SimpleMessageBack{msg.Val}); err != nil {
					log.Error(err)
				}
				return
			}
			// pass the message up
		case m := <-sp.backChan:
			msg := m.SimpleMessageBack
			// call the handler  if we are the root
			sp.counter++
			if sp.counter == len(sp.Children()) {
				if sp.IsRoot() {
					sp.handler(msg.Val)
				} else {
					sp.SendTo(sp.Parent(), &msg)
				}
				sp.Done()
				return
			}
		}
	}
}

// Client API request / response emulation
type simpleRequest struct {
	ServerIdentities *Roster
	Val              int
}

type simpleResponse struct {
	Val int
}

var simpleRequestType = network.RegisterMessageType(simpleRequest{})
var simpleResponseType = network.RegisterMessageType(simpleResponse{})

type simpleService struct {
	ctx *Context
}

func (s *simpleService) ProcessClientRequest(e *network.ServerIdentity, r *ClientRequest) {
	msgT, pm, err := network.UnmarshalRegisteredType(r.Data, network.DefaultConstructors(network.Suite))
	log.ErrFatal(err)
	if msgT != simpleRequestType {
		return
	}
	req := pm.(simpleRequest)
	tree := req.ServerIdentities.GenerateBinaryTree()
	tni := s.ctx.NewTreeNodeInstance(tree, tree.Root, "BackForth")
	proto, err := newBackForthProtocolRoot(tni, req.Val, func(n int) {
		if err := s.ctx.SendRaw(e, &simpleResponse{
			Val: n,
		}); err != nil {
			log.Error(err)
		}
	})
	if err != nil {
		log.Error(err)
		return
	}
	if err := s.ctx.RegisterProtocolInstance(proto); err != nil {
		log.Error(err)
	}
	go proto.Start()
}

func (s *simpleService) NewProtocol(tni *TreeNodeInstance, conf *GenericConfig) (ProtocolInstance, error) {
	pi, err := newBackForthProtocol(tni)
	return pi, err
}

func (s *simpleService) Process(packet *network.Packet) {
	return
}

func waitOrFatalValue(ch chan bool, v bool, t *testing.T) {
	select {
	case b := <-ch:
		if v != b {
			t.Fatal("Wrong value returned on channel")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Waited too long")
	}

}
func waitOrFatal(ch chan bool, t *testing.T) {
	select {
	case _ = <-ch:
		return
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Waited too long")
	}
}
