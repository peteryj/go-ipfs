package commands

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	core "github.com/ipfs/go-ipfs/core"

	cid "gx/ipfs/QmNp85zy9RLrQ5oQD4hPyS39ezrrXpcaa7R4Y9kxdWQLLQ/go-cid"
	cmdkit "gx/ipfs/QmPMeikDc7tQEDvaS66j1bVPQ2jBkvFwz3Qom5eA5i4xip/go-ipfs-cmdkit"
	pstore "gx/ipfs/QmPgDWmTmuzvP7QE5zwo1TmjbJme9pmZHNujB2453jkCTr/go-libp2p-peerstore"
	cmds "gx/ipfs/QmPhtZyjPYddJ8yGPWreisp47H6iQjt3Lg8sZrzqMP5noy/go-ipfs-cmds"
	blocks "gx/ipfs/QmSn9Td7xgxm9EV7iEjTckpUWmWApggzPxu7eFGWkkpwin/go-block-format"
	floodsub "gx/ipfs/Qmdnza7rLi7CMNNwNhNkcs9piX5sf6rxE8FrCsPzYtUEUi/floodsub"
)

var PubsubCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "An experimental publish-subscribe system on ipfs.",
		ShortDescription: `
ipfs pubsub allows you to publish messages to a given topic, and also to
subscribe to new messages on a given topic.

This is an experimental feature. It is not intended in its current state
to be used in a production environment.

To use, the daemon must be run with '--enable-pubsub-experiment'.
`,
	},
	Subcommands: map[string]*cmds.Command{
		"pub":   PubsubPubCmd,
		"sub":   PubsubSubCmd,
		"ls":    PubsubLsCmd,
		"peers": PubsubPeersCmd,
	},
}

var PubsubSubCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Subscribe to messages on a given topic.",
		ShortDescription: `
ipfs pubsub sub subscribes to messages on a given topic.

This is an experimental feature. It is not intended in its current state
to be used in a production environment.

To use, the daemon must be run with '--enable-pubsub-experiment'.
`,
		LongDescription: `
ipfs pubsub sub subscribes to messages on a given topic.

This is an experimental feature. It is not intended in its current state
to be used in a production environment.

To use, the daemon must be run with '--enable-pubsub-experiment'.

This command outputs data in the following encodings:
  * "json"
(Specified by the "--encoding" or "--enc" flag)
`,
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("topic", true, false, "String name of topic to subscribe to."),
	},
	Options: []cmdkit.Option{
		cmdkit.BoolOption("discover", "try to discover other peers subscribed to the same topic"),
	},
	Run: func(req cmds.Request, re cmds.ResponseEmitter) {
		n, err := req.InvocContext().GetNode()
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		// Must be online!
		if !n.OnlineMode() {
			re.SetError(errNotOnline, cmdkit.ErrClient)
			return
		}

		if n.Floodsub == nil {
			re.SetError(fmt.Errorf("experimental pubsub feature not enabled. Run daemon with --enable-pubsub-experiment to use."), cmdkit.ErrNormal)
			return
		}

		topic := req.Arguments()[0]
		sub, err := n.Floodsub.Subscribe(topic)
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}
		defer sub.Cancel()

		discover, _, _ := req.Option("discover").Bool()
		if discover {
			go func() {
				blk := blocks.NewBlock([]byte("floodsub:" + topic))
				cid, err := n.Blocks.AddBlock(blk)
				if err != nil {
					log.Error("pubsub discovery: ", err)
					return
				}

				connectToPubSubPeers(req.Context(), n, cid)
			}()
		}

		for {
			msg, err := sub.Next(req.Context())
			if err == io.EOF || err == context.Canceled {
				return
			} else if err != nil {
				re.SetError(err, cmdkit.ErrNormal)
				return
			}

			re.Emit(msg)
		}
	},
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.MakeEncoder(func(req cmds.Request, w io.Writer, v interface{}) error {
			m, ok := v.(*floodsub.Message)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			_, err := w.Write(m.Data)
			return err
		}),
		"ndpayload": cmds.MakeEncoder(func(req cmds.Request, w io.Writer, v interface{}) error {
			m, ok := v.(*floodsub.Message)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			m.Data = append(m.Data, '\n')
			_, err := w.Write(m.Data)
			return err
		}),
		"lenpayload": cmds.MakeEncoder(func(req cmds.Request, w io.Writer, v interface{}) error {
			m, ok := v.(*floodsub.Message)
			if !ok {
				return fmt.Errorf("unexpected type: %T", v)
			}

			buf := make([]byte, 8, len(m.Data)+8)

			n := binary.PutUvarint(buf, uint64(len(m.Data)))
			buf = append(buf[:n], m.Data...)
			_, err := w.Write(buf)
			return err
		}),
	},
	Type: floodsub.Message{},
}

func connectToPubSubPeers(ctx context.Context, n *core.IpfsNode, cid *cid.Cid) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	provs := n.Routing.FindProvidersAsync(ctx, cid, 10)
	wg := &sync.WaitGroup{}
	for p := range provs {
		wg.Add(1)
		go func(pi pstore.PeerInfo) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(ctx, time.Second*10)
			defer cancel()
			err := n.PeerHost.Connect(ctx, pi)
			if err != nil {
				log.Info("pubsub discover: ", err)
				return
			}
			log.Info("connected to pubsub peer:", pi.ID)
		}(p)
	}

	wg.Wait()
}

var PubsubPubCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "Publish a message to a given pubsub topic.",
		ShortDescription: `
ipfs pubsub pub publishes a message to a specified topic.

This is an experimental feature. It is not intended in its current state
to be used in a production environment.

To use, the daemon must be run with '--enable-pubsub-experiment'.
`,
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("topic", true, false, "Topic to publish to."),
		cmdkit.StringArg("data", true, true, "Payload of message to publish.").EnableStdin(),
	},
	Run: func(req cmds.Request, re cmds.ResponseEmitter) {
		n, err := req.InvocContext().GetNode()
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		// Must be online!
		if !n.OnlineMode() {
			re.SetError(errNotOnline, cmdkit.ErrClient)
			return
		}

		if n.Floodsub == nil {
			re.SetError("experimental pubsub feature not enabled. Run daemon with --enable-pubsub-experiment to use.", cmdkit.ErrNormal)
			return
		}

		topic := req.Arguments()[0]

		for _, data := range req.Arguments()[1:] {
			if err := n.Floodsub.Publish(topic, []byte(data)); err != nil {
				re.SetError(err, cmdkit.ErrNormal)
				return
			}
		}
	},
}

var PubsubLsCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "List subscribed topics by name.",
		ShortDescription: `
ipfs pubsub ls lists out the names of topics you are currently subscribed to.

This is an experimental feature. It is not intended in its current state
to be used in a production environment.

To use, the daemon must be run with '--enable-pubsub-experiment'.
`,
	},
	Run: func(req cmds.Request, re cmds.ResponseEmitter) {
		n, err := req.InvocContext().GetNode()
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		// Must be online!
		if !n.OnlineMode() {
			re.SetError(errNotOnline, cmdkit.ErrClient)
			return
		}

		if n.Floodsub == nil {
			re.SetError("experimental pubsub feature not enabled. Run daemon with --enable-pubsub-experiment to use.", cmdkit.ErrNormal)
			return
		}

		for _, topic := range n.Floodsub.GetTopics() {
			re.Emit(topic)
		}
	},
	Type: "",
}

var PubsubPeersCmd = &cmds.Command{
	Helptext: cmdkit.HelpText{
		Tagline: "List peers we are currently pubsubbing with.",
		ShortDescription: `
ipfs pubsub peers with no arguments lists out the pubsub peers you are
currently connected to. If given a topic, it will list connected
peers who are subscribed to the named topic.

This is an experimental feature. It is not intended in its current state
to be used in a production environment.

To use, the daemon must be run with '--enable-pubsub-experiment'.
`,
	},
	Arguments: []cmdkit.Argument{
		cmdkit.StringArg("topic", false, false, "topic to list connected peers of"),
	},
	Run: func(req cmds.Request, re cmds.ResponseEmitter) {
		n, err := req.InvocContext().GetNode()
		if err != nil {
			re.SetError(err, cmdkit.ErrNormal)
			return
		}

		// Must be online!
		if !n.OnlineMode() {
			re.SetError(errNotOnline, cmdkit.ErrClient)
			return
		}

		if n.Floodsub == nil {
			re.SetError(fmt.Errorf("experimental pubsub feature not enabled. Run daemon with --enable-pubsub-experiment to use."), cmdkit.ErrNormal)
			return
		}

		var topic string
		if len(req.Arguments()) == 1 {
			topic = req.Arguments()[0]
		}

		for _, peer := range n.Floodsub.ListPeers(topic) {
			re.Emit(peer.Pretty())
		}
	},
	Type: "",
	Encoders: cmds.EncoderMap{
		cmds.Text: cmds.Encoders[cmds.TextNewline],
	},
}
