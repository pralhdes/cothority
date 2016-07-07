// This is the ssh-keystore-server part that listens for requests of keystore-clients
// and will sign these requests.
package main

import (
	"github.com/codegangsta/cli"
	"github.com/dedis/cothority/log"
	"github.com/dedis/cothority/network"
	"github.com/dedis/cothority/services/identity"

	"os"

	"os/user"
	"strings"

	"encoding/hex"

	"io/ioutil"

	"bytes"

	"github.com/dedis/cothority/app/lib/config"
	"github.com/dedis/cothority/app/lib/ui"
)

type servers struct {
	PathSSH string
	IDs     []*identity.Identity
}

var serverKS = &servers{}

var configFile string

func main() {
	network.RegisterMessageType(servers{})
	app := cli.NewApp()
	app.Name = "SSH keystore server"
	app.Usage = "Serves as a server to listen to requests"
	app.Commands = []cli.Command{
		{
			Name:      "addId",
			Aliases:   []string{"a"},
			Usage:     "adding a new identity",
			Action:    addID,
			ArgsUsage: "group-file identity-hash",
		},
		{
			Name:      "delId",
			Aliases:   []string{"d"},
			Usage:     "delete an identity",
			Action:    delID,
			ArgsUsage: "identity-hash",
		},
		{
			Name:    "update",
			Aliases: []string{"u"},
			Usage:   "update to the latest list",
			Action:  update,
		},
		{
			Name:    "list",
			Aliases: []string{"l"},
			Usage:   "lists all identities and their accounts",
			Action:  list,
		},
	}
	app.Flags = []cli.Flag{
		cli.IntFlag{
			Name:  "debug, d",
			Value: 1,
			Usage: "debug-level: 1 for terse, 5 for maximal",
		},
		cli.StringFlag{
			Name:  "config, cs",
			Value: "~/.ssh",
			Usage: "The configuration-directory of the ssh-directory",
		},
	}
	app.Before = func(c *cli.Context) error {
		configDir := tildeToHome(c.String("config"))
		os.Mkdir(configDir, 0660)
		log.SetDebugVisible(c.Int("debug"))
		configFile = configDir + "/config.bin"
		if err := loadConfig(); err != nil {
			ui.Error("Problems reading config-file. Most probably you\n",
				"should start a new one by running with the 'setup'\n",
				"argument.")
		}
		return nil
	}
	app.After = func(c *cli.Context) error {
		if len(serverKS.IDs) > 0 {
			saveConfig()
		}
		return nil
	}
	app.Run(os.Args)
}

func loadConfig() error {
	buf, err := ioutil.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	_, msg, err := network.UnmarshalRegistered(buf)
	if err != nil {
		return err
	}
	serverKS = msg.(*servers)
	return nil
}

func saveConfig() {
	buf, err := network.MarshalRegisteredType(serverKS)
	ui.ErrFatal(err, "Couldn't marshal servers")
	ui.ErrFatal(ioutil.WriteFile(configFile, buf, 0660))
	return
}

func delID(c *cli.Context) {
	idHex, err := hex.DecodeString(c.Args().First())
	ui.ErrFatal(err)
	idFound := -1
	for idCnt, id := range serverKS.IDs {
		if bytes.Equal(id.ID, idHex) {
			ui.Infof("Will delete id %x", idHex)
			idFound = idCnt
		}
	}
	if idFound == -1 {
		ui.Errorf("Didn't find ID %x", idHex)
		list(c)
		return
	}
	updateAllow(false)
	serverKS.IDs = append(serverKS.IDs[:idFound], serverKS.IDs[idFound+1:]...)
	updateAllow(true)
	list(c)
}

func addID(c *cli.Context) {
	groupFile := tildeToHome(c.Args().Get(0))
	idStr := c.Args().Get(1)
	if groupFile == "" {
		ui.Fatal("Please indicate the group-file to use")
	}
	if idStr == "" {
		ui.Fatal("Please inidicate what ID to follow")
	}

	updateAllow(false)
	reader, err := os.Open(groupFile)
	ui.ErrFatal(err, "Didn't find group-file: ", groupFile)
	el, err := config.ReadGroupToml(reader)
	reader.Close()
	ui.ErrFatal(err, "Couldn't read group-file")
	if el == nil {
		ui.Fatal("Group-file didn't contain any entities")
	}

	id, err := hex.DecodeString(idStr)
	ui.ErrFatal(err, "Couldn't convert id to hex")
	iden, err := identity.NewIdentityFromCothority(el, id)
	ui.ErrFatal(err, "Couldn't get identity")
	serverKS.IDs = append(serverKS.IDs, iden)
	serverKS.PathSSH = tildeToHome(c.GlobalString("config"))
	updateAllow(true)
	list(c)
}

func update(c *cli.Context) {
	updateAllow(false)
	for _, s := range serverKS.IDs {
		ui.ErrFatal(s.ConfigUpdate())
	}
	updateAllow(true)
	list(c)
}

func updateAllow(add bool) {
	ak := serverKS.PathSSH + "/authorized_keys"
	for _, id := range serverKS.IDs {
		for _, ssh := range id.Config.Data {
			if add {
				if !grep(ak, ssh) {
					addLine(ak, ssh)
				}
			} else {
				deleteLine(ak, ssh)
			}
		}
	}
}

func grep(fileName, str string) bool {
	buf, err := ioutil.ReadFile(fileName)
	if err != nil {
		return false
	}
	return bytes.Contains(buf, []byte(str))
}

func addLine(file, str string) {
	content, _ := ioutil.ReadFile(file)
	found := false
	for _, l := range strings.Split(string(content), "\n") {
		found = found || (l == str)
	}
	if !found {
		concat := append(content, []byte(str+"\n")...)
		err := ioutil.WriteFile(file, concat, 0660)
		ui.ErrFatal(err)
	}
}

func deleteLine(file, strOrig string) {
	str := strings.TrimSpace(strOrig)
	if !grep(file, str) {
		return
	}
	var lines []string
	content, err := ioutil.ReadFile(file)
	ui.ErrFatal(err)
	for _, l := range strings.Split(string(content), "\n") {
		if l != str {
			lines = append(lines, l)
		}
	}
	err = ioutil.WriteFile(file, []byte(strings.Join(lines, "\n")), 0660)
	ui.ErrFatal(err)
}

func list(c *cli.Context) {
	for i, s := range serverKS.IDs {
		ui.Infof("Server %d: %s", i, s.Config)
	}
}

func tildeToHome(path string) string {
	if strings.HasPrefix(path, "~") {
		usr, err := user.Current()
		ui.ErrFatal(err)
		return usr.HomeDir + path[1:len(path)]
	}
	return path
}