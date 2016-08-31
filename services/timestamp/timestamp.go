// timestamp contains a simplified timestamp server. It collects statements from
// clients, waits EpochDuration time and responds with a signature of the
// requested data.
package timestamp

import (
	"errors"
	"fmt"
	"time"

	"crypto/sha256"
	"os"
	"path/filepath"
	"sync"

	"encoding/binary"
	"github.com/dedis/cothority/app/lib/config"
	"github.com/dedis/cothority/crypto"
	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/network"
	"github.com/dedis/cothority/protocols/swupdate"
	"github.com/dedis/cothority/sda"
)

// ServiceName can be used to refer to the name of the timestamp service
const ServiceName = "Timestamp"

// TODO make this a config parameter:
const EpochDuration = (time.Second * 10)

const groupFileName = "group.toml"

var timestampSID sda.ServiceID

var dummyVerfier = func(data []byte) bool {
	log.Print("Got time", string(data))
	return true
}

func init() {
	sda.RegisterNewService(ServiceName, newTimestampService)
	timestampSID = sda.ServiceFactory.ServiceID(ServiceName)
	network.RegisterPacketType(&SignatureRequest{})
	network.RegisterPacketType(&SignatureResponse{})
}

type tree struct {
	proofs []crypto.Proof
	root   crypto.HashID
}

type requestPool struct {
	sync.Mutex
	requestData []crypto.HashID
}

func (rb *requestPool) reset() {
	rb.Lock()
	defer rb.Unlock()
	rb.requestData = nil
}

func (rb *requestPool) Add(data []byte) {
	rb.Lock()
	defer rb.Unlock()
	rb.requestData = append(rb.requestData, data)
}

func (rb *requestPool) GetData() []byte {
	rb.Lock()
	defer rb.Unlock()
	return rb.requestData
}

type Service struct {
	*sda.ServiceProcessor
	// Epoch is is the time that needs to pass until
	// the timestamp service attempts to collectively sign the batches
	// of statements collected. Reasonable choices would be from 10 seconds
	// upto some hours.
	EpochDuration time.Duration

	// config path for service:
	path string
	// collected data for one epoch:
	requests requestPool
	roster   *sda.Roster

	currentTree *tree
	currentSig  []byte
}

// NewProtocol is called on all nodes of a Tree (except the root, since it is
// the one starting the protocol) so it's the Service that will be called to
// generate the PI on all others node.
func (s *Service) NewProtocol(tn *sda.TreeNodeInstance, conf *sda.GenericConfig) (sda.ProtocolInstance, error) {
	log.Lvl2("Timestamp Service received New Protocol event")
	var pi sda.ProtocolInstance
	var err error
	// TODO does this work? Maybe each node should have a unique protocol
	// name instead
	sda.ProtocolRegisterName("UpdateCosi", func(n *sda.TreeNodeInstance) (sda.ProtocolInstance, error) {
		// XXX for now we provide a dummy verification function. It
		// just prints out the timestamp, received in the Announcement.
		return swupdate.NewCoSiUpdate(n, dummyVerfier)
	})
	return pi, err
}

// SignatureRequest will be requested by clients.
type SignatureRequest struct {
	// Message should be a hashed nonce for the timestamp server.
	Message []byte
	// Different requests will be signed by the same roster
	// Hence, it doesn't make sense for every client to send his Roster
	// Roster  *sda.Roster
}

// SignatureResponse is what the Cosi service will reply to clients.
type SignatureResponse struct {
	// The time in seconds when the request was started
	Timestamp int32
	// Proof is an Inclusion proof for the data the client requested
	Proof crypto.Proof
	// Collective signature on Timestamp||hash(treeroot)
	Signature []byte
	roster    *sda.Roster
}

// SignatureRequest treats external request to this service.
func (s *Service) SignatureRequest(si *network.ServerIdentity, req *SignatureRequest) (network.Body, error) {
	// TODO is this blocking??? If yes this needs to happen in yet another
	// go-routine.

	// on every request:
	// 1) If has the length of hashed nonce, add it to the local buffer of
	//    of the service:
	s.requests.Add(req.Message)
	// 2) At epoch time: create the merkle tree
	// see runLoop
	// 3) run *one* cosi round on treeroot||timestamp
	// see runLoop
	// 4) return to each client: above signature, (inclusion) Proof, timestamp

	return &SignatureResponse{
		// TODO timestamp
		// TODO sort out correct Proof for client
		//Sum:       req.Message,
		Signature: s.currentSig,
	}, nil
}

func (s *Service) runLoop() {
	go func() {
		c := time.Tick(s.EpochDuration)
		for now := range c {
			log.Print("Starting cosi: ", now, sha256.BlockSize)

			sdaTree := s.roster.GenerateBinaryTree()
			tni := s.NewTreeNodeInstance(sdaTree, sdaTree.Root, swupdate.ProtcolName)
			pi, err := swupdate.NewCoSiUpdate(tni, dummyVerfier)
			if err != nil {
				return nil, errors.New("Couldn't make new protocol: " + err.Error())
			}
			s.RegisterProtocolInstance(pi)
			root, proofs := crypto.ProofTree(sha256.New, s.requests.GetData())
			s.currentTree = &tree{
				root:   root,
				proofs: proofs,
			}

			timeBuf := make([]byte, binary.MaxVarintLen64)
			binary.PutVarint(timeBuf, now.Unix())
			// message to be signed: treeroot||timestamp
			msg := append(root, timeBuf)
			pi.SigningMessage(msg)
			// Take the raw message (already expecting a hash for the timestamp
			// service)
			response := make(chan []byte)
			pi.RegisterSignatureHook(func(sig []byte) {
				response <- sig
				s.currentSig = sig
			})
			log.Lvl3("Cosi Service starting up root protocol")
			go pi.Dispatch()
			go pi.Start()
			fmt.Printf("%s: Signed a message.\n", time.Now().Format("Mon Jan 2 15:04:05 -0700 MST 2006"))

		}
	}()
}

func newTimestampService(c *sda.Context, path string) sda.Service {
	r, err := readRoster(filepath.Join(path, groupFileName))
	if err != nil {
		log.ErrFatal(err,
			"Couldn't read roster from config. Put a valid roster definition in",
			filepath.Join(path, groupFileName))
	}
	s := &Service{
		ServiceProcessor: sda.NewServiceProcessor(c),
		path:             path,
		requests:         requestPool{},
		EpochDuration:    EpochDuration,
		roster:           r,
	}
	err = s.RegisterMessage(s.SignatureRequest)
	if err != nil {
		log.ErrFatal(err, "Couldn't register message:")
	}
	s.runLoop()
	return s
}

func readRoster(tomlFile string) (*sda.Roster, error) {
	f, err := os.Open(tomlFile)
	if err != nil {
		return nil, err
	}
	el, err := config.ReadGroupToml(f)
	if err != nil {
		return nil, err
	}
	return el, nil
}
