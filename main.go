package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	uuid "github.com/nu7hatch/gouuid"

	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/ad/gocc/proto"
)

const version = "0.4.21"

var (
	port       string
	gogeoAddr  string
	serverUUID string
	su, _      = uuid.NewV4()
	fqdn       = FQDN()
)

type Listener struct {
	SS grpc.ServerStream
}

func (ss *Listener) SendZondRequest(req *proto.MessageRequest) error {
	return ss.SS.SendMsg(req)
}

var allClients = make(map[string]*Listener)

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)

	flag.StringVar(&port, "port", LookupEnvOrString("GOCC_PORT", "9000"), "Port to listen on")
	flag.StringVar(&gogeoAddr, "gogeoaddr", LookupEnvOrString("GOCC_GOGEOADDR", "http://127.0.0.1:9001"), "proto://address:port of gogeo instance")
	flag.StringVar(&serverUUID, "uuid", LookupEnvOrString("GOCC_UUID", su.String()), "server uuid")

	flag.Parse()
	rand.Seed(time.Now().UnixNano())
}

type srv struct{}

func main() {
	log.Printf("Started version %s at %s", version, fqdn)

	lis, err := net.Listen("tcp", ":5000")
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// create grpc server
	// Create an array of gRPC options with the credentials
	opts := []grpc.ServerOption{grpc.StreamInterceptor(streamInterceptor)} // create a gRPC server object
	s := grpc.NewServer(opts...)
	proto.RegisterZondServer(s, srv{})

	log.Printf("listening grpc on port %d", 5000)

	// and start...
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	go StartSelfupdate("ad/gocc", version, fqdn)

	resetProcessingTicker := time.NewTicker(60 * time.Second)
	go func(resetProcessingTicker *time.Ticker) {
		for {
			select {
			case <-resetProcessingTicker.C:
				ResetProcessing()
			}
		}
	}(resetProcessingTicker)

	checkAliveTicker := time.NewTicker(60 * time.Second)
	go func(checkAliveTicker *time.Ticker) {
		for {
			select {
			case <-checkAliveTicker.C:
				CheckAlive()
			}
		}
	}(checkAliveTicker)

	resendRepeatableTicker := time.NewTicker(60 * time.Second)
	go func(resendRepeatableTicker *time.Ticker) {
		for {
			select {
			case <-resendRepeatableTicker.C:
				ResendRepeatable(false)
			}
		}
	}(resendRepeatableTicker)

	getActiveDestinationsTicker := time.NewTicker(120 * time.Second)
	go func(getActiveDestinationsTicker *time.Ticker) {
		for {
			select {
			case <-getActiveDestinationsTicker.C:
				GetActiveDestinations()
			}
		}
	}(getActiveDestinationsTicker)

	r := mux.NewRouter()

	r.Handle("/", Throttle(time.Minute, 60, http.HandlerFunc(GetHandler))).Methods("GET")
	r.Handle("/auth", http.HandlerFunc(AuthHandler))
	r.HandleFunc("/dispatch/", func(w http.ResponseWriter, r *http.Request) { DispatchHandler(w, r, gogeoAddr) })
	r.Handle("/task/create", Throttle(time.Minute, 10, http.HandlerFunc(ShowCreateForm))).Methods("GET")
	r.Handle("/version", Throttle(time.Minute, 60, http.HandlerFunc(ShowVersion))).Methods("GET")

	r.Handle("/user", Throttle(time.Minute, 60, http.HandlerFunc(UserInfoHandler))).Methods("GET")
	r.Handle("/user/auth", Throttle(time.Minute, 60, http.HandlerFunc(UserAuthHandler)))
	r.Handle("/recover", Throttle(time.Minute, 3, http.HandlerFunc(UserRecoverHandler)))
	r.Handle("/reset", Throttle(time.Minute, 3, http.HandlerFunc(UserResetHandler)))
	r.Handle("/login", Throttle(time.Minute, 5, http.HandlerFunc(UserLoginHandler)))
	r.Handle("/register", Throttle(time.Minute, 5, http.HandlerFunc(UserRegisterHandler)))

	r.Handle("/api/token", Throttle(time.Minute, 60, http.HandlerFunc(ApiTokenHandler))).Methods("GET")

	r.Handle("/task/my", Throttle(time.Minute, 60, http.HandlerFunc(ShowMyTasks))).Methods("GET")
	r.Handle("/api/task/my", Throttle(time.Minute, 60, http.HandlerFunc(ApiShowMyTasks))).Methods("GET")

	r.Handle("/zond/my", Throttle(time.Minute, 60, http.HandlerFunc(ShowMyZonds))).Methods("GET")
	r.Handle("/api/zond/my", Throttle(time.Minute, 60, http.HandlerFunc(ApiShowMyZonds))).Methods("GET")

	r.Handle("/mngr/my", Throttle(time.Minute, 60, http.HandlerFunc(ShowMyMngrs))).Methods("GET")
	r.Handle("/api/mngr/my", Throttle(time.Minute, 60, http.HandlerFunc(ApiShowMyMngrs))).Methods("GET")

	r.Handle("/task/repeatable", Throttle(time.Minute, 60, http.HandlerFunc(ShowRepeatableTasks))).Methods("GET")
	r.Handle("/api/task/repeatable", Throttle(time.Minute, 60, http.HandlerFunc(ApiShowRepeatableTasks))).Methods("GET")

	r.Handle("/api/task/create", Throttle(time.Minute, 10, http.HandlerFunc(ApiTaskCreateHandler))).Methods("POST")
	r.Handle("/api/zond/create", Throttle(time.Minute, 10, http.HandlerFunc(ApiZondCreateHandler))).Methods("POST")
	r.Handle("/api/mngr/create", Throttle(time.Minute, 10, http.HandlerFunc(ApiMngrCreateHandler))).Methods("POST")
	r.Handle("/api/task/repeatable/remove", Throttle(time.Minute, 10, http.HandlerFunc(ApiTaskRepeatableRemoveHandler))).Methods("POST")

	// requests from zonds
	r.Handle("/zond/task/block", Throttle(time.Minute, 60, ZondAuth(http.HandlerFunc(TaskZondBlockHandler)))).Methods("POST")
	r.Handle("/zond/task/result", Throttle(time.Minute, 60, ZondAuth(http.HandlerFunc(TaskZondResultHandler)))).Methods("POST")
	r.Handle("/zond/pong", Throttle(time.Minute, 15, ZondAuth(http.HandlerFunc(ZondPong)))).Methods("POST")

	// internal requests
	r.Handle("/zond/sub", Throttle(time.Minute, 60, http.HandlerFunc(ZondSub))).Methods("GET")
	r.Handle("/zond/unsub", Throttle(time.Minute, 60, http.HandlerFunc(ZondUnsub))).Methods("GET")

	r.Handle("/mngr/my", Throttle(time.Minute, 60, http.HandlerFunc(ShowMyMngrs)))

	// requests from managers
	r.Handle("/mngr/task/block", MngrAuth(http.HandlerFunc(TaskMngrBlockHandler))).Methods("POST")
	r.Handle("/mngr/task/result", MngrAuth(http.HandlerFunc(TaskMngrResultHandler))).Methods("POST")
	r.Handle("/mngr/pong", Throttle(time.Minute, 5, MngrAuth(http.HandlerFunc(MngrPong)))).Methods("POST")

	// internal requests
	r.Handle("/mngr/sub", Throttle(time.Minute, 60, http.HandlerFunc(MngrSub))).Methods("GET")
	r.Handle("/mngr/unsub", Throttle(time.Minute, 60, http.HandlerFunc(MngrUnsub))).Methods("GET")

	r.NotFoundHandler = http.HandlerFunc(NotFound)

	CSRF := csrf.Protect(
		[]byte(RandStr(32)),
		csrf.FieldName("token"),
		csrf.Secure(false), // NB: REMOVE IN PRODUCTION!
		csrf.Path("/"),
	)

	skipCheck := func(h http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			for _, path := range []string{"/zond/task", "/zond/pong", "/mngr/task", "/mngr/pong"} {
				if strings.HasPrefix(r.URL.Path, path) {
					r = csrf.UnsafeSkipCheck(r)
				}
			}
			h.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}

	loggingHandler := func(h http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			t := time.Now()
			h.ServeHTTP(w, r)
			elapsed := time.Since(t)
			UUID := r.Header.Get("X-ZondUuid")
			if UUID == "" {
				UUID = r.Header.Get("X-MngrUuid")
			}
			fmt.Printf(
				"%s - %s%s - [%s] \"%s %s %s\" %s\n",
				r.Header.Get("X-Forwarded-For"),
				UUID,
				r.Header.Get("X-Forwarded-User"),
				t.Format("02/Jan/2006:15:04:05 -0700"),
				r.Method,
				r.RequestURI,
				r.Proto,
				elapsed,
			)
		}
		return http.HandlerFunc(fn)
	}

	log.Printf("listening on port %s", port)

	go ResendOffline()
	go ResendRepeatable(true)

	go func() {
		pingTicker := time.NewTicker(60 * time.Second)
		for {
			select {
			case _ = <-pingTicker.C:
				for zond := range allClients {
					req := proto.MessageRequest{ZondUUID: zond, Action: "alive"}
					if err := allClients[zond].SendZondRequest(&req); err != nil {
						fmt.Printf("can not send %v", err)
						delete(allClients, zond)
					}
				}
			}
		}
	}()

	log.Fatal(http.ListenAndServe("127.0.0.1:"+port, skipCheck(CSRF(loggingHandler(r)))))
}

// unaryInterceptor calls authenticateClient with current context
func streamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	// authentication (token verification)
	md, ok := metadata.FromIncomingContext(ss.Context())
	if !ok {
		return fmt.Errorf("metadata not found")
	}

	log.Println("request", info, md)
	if val, ok := md["zonduuid"]; ok && len(val) == 1 {
		log.Println("request FROM", val[0])

		allClients[val[0]] = &Listener{SS: ss}
	}

	// log.Println("request", info)
	// s, ok := info.Server.(*api.Server)
	// if !ok {
	// 	return nil, fmt.Errorf("unable to cast server")
	// }
	// clientID, err := authenticateClient(ctx, s)
	// if err != nil {
	// 	return nil, err
	// }
	// ctx = context.WithValue(ctx, clientIDKey, clientID)
	return handler(srv, ss)
}

func (s srv) Message(srv proto.Zond_MessageServer) error {
	// fmt.Printf("%+v\n", req)

	log.Println("Message...")
	// var max int32
	ctx := srv.Context()

	for {

		// exit if context is done
		// or continue
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// receive data from stream
		req, err := srv.Recv()
		if err == io.EOF {
			// return will close stream from server side
			log.Println("exit")
			return nil
		}
		if err != nil {
			log.Printf("receive error %v", err)
			continue
		}

		log.Printf("received %+v", req)

		// continue if number reveived from stream
		// less than max
		// if req.Num <= max {
		// 	continue
		// }

		// update max and send it to stream
		// max = req.Num
		resp := proto.MessageResponse{Status: "ok"}
		if err := srv.Send(&resp); err != nil {
			log.Printf("send error %v", err)
		}

		// request := proto.CallRequest{Action: "from server"}
		// if err := srv.Send(&request); err != nil {
		// 	log.Printf("send error %v", err)
		// }
		// log.Printf("send new max=%d", max)
	}

	// if req.Action != "alive" {
	// 	fmt.Printf("%+v\n", req)
	// }

	// if req.Action == "ping" {
	// 	pingCheck(req.Param, req.UUID)
	// } else if req.Action == "head" {
	// 	headCheck(req.Param, req.UUID)
	// } else if req.Action == "dns" {
	// 	dnsCheck(req.Param, req.UUID)
	// } else if req.Action == "traceroute" {
	// 	tracerouteCheck(req.Param, req.UUID)
	// } else if req.Action == "alive" {
	// 	if !pongStarted.IsSet() {
	// 		pongStarted.Set()

	// 		req.ZondUUID = *zonduuid
	// 		js, _ := json.Marshal(req)

	// 		Post("http://"+*addr+"/zond/pong", string(js))

	// 		pongStarted.UnSet()
	// 	}
	// }
	// return &proto.CallResponse{Status: "success"}, nil
}
