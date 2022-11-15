package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	p2p "p2p/grpc"
	"sort"
	"strconv"
	"time"

	"google.golang.org/grpc"
)

type peer struct {
	p2p.UnimplementedPeerServiceServer
	Port             int32
	Clients          map[int32]p2p.PeerServiceClient
	State            State
	LamportTimestamp int32
	CSQueue          map[int32]int32
	ctx              context.Context
}

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

	// New server
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

		// Dial
		var conn *grpc.ClientConn
		conn, err := grpc.Dial(fmt.Sprintf(":%v", nodePort), grpc.WithInsecure(), grpc.WithBlock())
		defer conn.Close()

		if err != nil {
			log.Fatalf("Failed connecting to %v: %s", nodePort, err)
		}

		client := p2p.NewPeerServiceClient(conn)
		peer.Clients[nodePort] = client
		peer.LamportTimestamp += 1

		fmt.Printf("|-(%v) Successfully connected to %v \n", peer.LamportTimestamp, nodePort)
	}

	// Aggree with network of clients on "enter"
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		peer.AskNetworkForPermission()
	}

}

func (p *peer) AskNetworkForPermission() {
	allowed := true
	p.SetState(WANTED)

	fmt.Println("Asking peers for permission:")
	
	// Ask all clients in network
	for port, client := range p.Clients {
		
		p.LamportTimestamp += 1
		req := &p2p.Request{
			Port:             p.Port,
			LamportTimestamp: p.LamportTimestamp,
		}

		reply, err := client.AskPermission(p.ctx, req)
		
		if err != nil {
			log.Fatalf("Error in AskPermission: %v", err)
		}

		p.LamportTimestamp = max(p.LamportTimestamp, reply.LamportTimestamp) + 1

		if reply.Answer == false {
			fmt.Printf("|-(%v) Permission denied by Client:%v \n", p.LamportTimestamp, port)
			allowed = false
		}

		if reply.Answer == true {
			fmt.Printf("|-(%v) Successfully got permission by Client:%v \n", p.LamportTimestamp, port)
		}
	}

	if allowed == true {
		p.EnterCriticalSection()
	} else {
		fmt.Printf("|-(%v) Waiting for my turn to access TCS...\n", p.LamportTimestamp)
	}
}

func (p *peer) SetState(s State) {
	p.State = s
}

func (p *peer) EnterCriticalSection() {
	p.SetState(HELD)
	defer p.SetState(FREE)
	fmt.Printf("|-(%v) ** Accessing TCS! **\n", p.LamportTimestamp)
	time.Sleep(time.Second * 5) // Hold for 5 sec.
	fmt.Printf("|-(%v) ** Left TCS! **\n", p.LamportTimestamp)
	
	p.LamportTimestamp += 1

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
			p.LamportTimestamp += 1
			fmt.Printf("|-(%v) Passing access to Client:%v in queue! \n", p.LamportTimestamp, queuePorts[i])
			clientFromPort := p.Clients[queuePorts[i]]
			clientFromPort.GivePermission(p.ctx, &p2p.Request{Port: p.Port, LamportTimestamp: p.LamportTimestamp})
		}
	}
}

func (p *peer) GivePermission(ctx context.Context, in *p2p.Request) (*p2p.Request, error) {

	p.LamportTimestamp = max(p.LamportTimestamp, in.LamportTimestamp) + 1

	fmt.Printf("|-(%v) Permission to TCS granted by Client:%v \n", p.LamportTimestamp, in.Port)
	if p.State == WANTED {
		p.EnterCriticalSection()
	} else {
		fmt.Printf("|-(%v) State is no longer wanted \n", p.LamportTimestamp)
	}
	return &p2p.Request{}, nil
}

func (p *peer) AskPermission(ctx context.Context, in *p2p.Request) (*p2p.Reply, error) {

	p.LamportTimestamp = max(p.LamportTimestamp, in.LamportTimestamp) + 1

	if p.State == HELD {
		fmt.Printf("|-(%v) Added Client:%v to queue \n", p.LamportTimestamp, in.Port)
		p.CSQueue[in.Port] = in.LamportTimestamp
		return &p2p.Reply{Port: p.Port, Answer: false, LamportTimestamp: p.LamportTimestamp}, nil 
	}
	if p.State == WANTED {
		if in.LamportTimestamp < p.LamportTimestamp {
			return &p2p.Reply{Port: p.Port, Answer: true, LamportTimestamp: p.LamportTimestamp}, nil 
		} else if in.LamportTimestamp == p.LamportTimestamp {
			if in.Port < p.Port {
				return &p2p.Reply{Port: p.Port, Answer: true, LamportTimestamp: p.LamportTimestamp}, nil
			} else {
				fmt.Printf("|-(%v) Added Client:%v to queue \n", p.LamportTimestamp, in.Port)
				p.CSQueue[in.Port] = in.LamportTimestamp
				return &p2p.Reply{Port: p.Port, Answer: false, LamportTimestamp: p.LamportTimestamp}, nil
			}
		} else {
			fmt.Printf("|-(%v) Added Client:%v to queue \n", p.LamportTimestamp, in.Port)
			p.CSQueue[in.Port] = in.LamportTimestamp
			return &p2p.Reply{Port: p.Port, Answer: false, LamportTimestamp: p.LamportTimestamp}, nil
		}
	}
	return &p2p.Reply{Port: p.Port, Answer: true, LamportTimestamp: p.LamportTimestamp}, nil
}

func max(ownTimestamp int32, theirTimestamp int32) int32 {
	return int32(math.Max(float64(ownTimestamp), float64(theirTimestamp)))
}