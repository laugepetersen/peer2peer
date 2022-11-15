package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	p2p "p2p/grpc"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc"
)

type State string

const (
	FREE   State = "free"
	WANTED State = "wanted"
	HELD   State = "held"
)

func main() {

	// Retrieve network ports from terminal args
	port, _ := strconv.ParseInt(os.Args[1], 10, 32)
	ports := make([]int32, 0)
	for i := 2; i < len(os.Args); i++ {
		arg, _ := strconv.ParseInt(os.Args[i], 10, 32)
		ports = append(ports, int32(arg))
	}

	// Don't touch
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Instantiate Peer
	peer := &peer{
		Port:             int32(port),
		Clients:          make(map[int32]p2p.PeerServiceClient),
		State:            FREE,
		CSQueue:          make(map[int32]int32),
		LamportTimestamp: 0,
		ctx:              ctx,
	}

	// TCP-listener to port
	list, err := net.Listen("tcp", fmt.Sprintf(":%v", port))

	if err != nil {
		log.Fatalf("Failed to listen on port: %v", err)
	}

	grpcServer := grpc.NewServer()
	p2p.RegisterPeerServiceServer(grpcServer, peer)

	go func() {
		if err := grpcServer.Serve(list); err != nil {
			log.Fatalf("Failed to serve %v", err)
		}
	}()

	fmt.Printf("Listening on port %v \n", port)
	fmt.Printf("Dialing services: \n")

	// Discover other clients/services
	for i := 0; i < len(ports); i++ {
		nodePort := ports[i]

		if nodePort == int32(port) {
			continue
		}

		// Don't touch
		var conn *grpc.ClientConn
		conn, err := grpc.Dial(fmt.Sprintf(":%v", nodePort), grpc.WithInsecure(), grpc.WithBlock())
		defer conn.Close()

		if err != nil {
			log.Fatalf("Failed connecting to %v: %s", nodePort, err)
		}

		client := p2p.NewPeerServiceClient(conn)
		peer.Clients[nodePort] = client
		peer.LamportTimestamp += 1

		fmt.Printf("|– Successfully connected to %v \n", nodePort)
	}

	// Aggree with network of clients
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		peer.AskNetworkForPermission()
	}

}

func (p *peer) AskNetworkForPermission() {
	allowed := true
	p.SetState(WANTED)

	fmt.Println("Asking peers for permision:")

	// Ask all clients in network
	for port, client := range p.Clients {
		req := &p2p.Request{
			Port:             p.Port,
			LamportTimestamp: p.LamportTimestamp,
		}

		reply, err := client.AskPermission(p.ctx, req)

		if err != nil {
			log.Fatalf("Error in AskPermission: %v", err)
		}

		if reply.Answer == false {
			fmt.Printf("|- Permisson denied by Client:%v \n", port)
			allowed = false
		}

		if reply.Answer == true {
			fmt.Printf("|- Successfully got permisson by Client:%v \n", port)
		}
	}

	if allowed == true {
		p.EnterCriticalSection()
	} else {
		fmt.Println("|- Waiting for my turn to access TCS...")
	}
}

func (p *peer) SetState(s State) {
	p.State = s
}

func (p *peer) EnterCriticalSection() {
	p.SetState(HELD)
	defer p.SetState(FREE)
	fmt.Println("|- ** Accessing TCS! **")
	time.Sleep(time.Second * 5) // Hold for 5 sec.
	fmt.Println("|- ** Left TCS! **")

	// Grant access to waiting client next in queue
	if len(p.CSQueue) > 0 {
		queuePorts := make([]int32, 0, len(p.CSQueue))
		for port := range p.CSQueue {
			queuePorts = append(queuePorts, port)
		}
		sort.Slice(queuePorts, func(i, j int) bool {
			return p.CSQueue[queuePorts[i]] < p.CSQueue[queuePorts[j]]
		})
		for i := 0; i < len(queuePorts); i++ {
			fmt.Printf("|- Passing access to Client:%v in queue! \n", queuePorts[i])
			clientFromPort := p.Clients[queuePorts[i]]
			clientFromPort.GivePermission(p.ctx, &p2p.Request{Port: p.Port, LamportTimestamp: p.LamportTimestamp})
		}
	}
}

func (p *peer) GivePermission(ctx context.Context, in *p2p.Request) (*p2p.Request, error) {
	fmt.Printf("|- Permission to TCS granted by Client:%v \n", in.Port)
	if p.State == WANTED {
		p.EnterCriticalSection()
	} else {
		fmt.Printf("|- State is no longer wanted \n")
	}
	return &p2p.Request{}, nil
}

func (p *peer) AskPermission(ctx context.Context, in *p2p.Request) (*p2p.Reply, error) {
	if p.State == HELD {
		fmt.Printf("|- Added Client:%v to queue \n", in.Port)
		p.CSQueue[in.Port] = in.LamportTimestamp
		return &p2p.Reply{Port: p.Port, Answer: false}, nil
	}
	if p.State == WANTED {
		if in.LamportTimestamp < p.LamportTimestamp {
			return &p2p.Reply{Port: p.Port, Answer: true}, nil
		} else if in.LamportTimestamp == p.LamportTimestamp {
			if in.Port < p.Port {
				return &p2p.Reply{Port: p.Port, Answer: true}, nil
			} else {
				fmt.Printf("|- Added Client:%v to queue \n", in.Port)
				p.CSQueue[in.Port] = in.LamportTimestamp
				return &p2p.Reply{Port: p.Port, Answer: false}, nil
			}
		} else {
			fmt.Printf("|- Added Client:%v to queue \n", in.Port)
			p.CSQueue[in.Port] = in.LamportTimestamp
			return &p2p.Reply{Port: p.Port, Answer: false}, nil
		}
	}
	return &p2p.Reply{Port: p.Port, Answer: true}, nil
}

type peer struct {
	p2p.UnimplementedPeerServiceServer
	Port             int32
	Clients          map[int32]p2p.PeerServiceClient
	State            State
	LamportTimestamp int32
	CSQueue          map[int32]int32
	ctx              context.Context
}
