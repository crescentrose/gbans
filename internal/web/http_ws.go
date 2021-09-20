package web

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/leighmacdonald/gbans/internal/consts"
	"github.com/leighmacdonald/gbans/internal/event"
	"github.com/leighmacdonald/gbans/internal/model"
	"github.com/leighmacdonald/gbans/internal/store"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"gopkg.in/olahol/melody.v1"
	"sync"
	"time"
)

type PayloadType int

const (
	sendQueueSize = 100
	recvQueueSize = 100
)

type State int32

const (
	Closed State = iota
	Opened
	AwaitingAuthentication
	Authenticated
	Closing
)

const (
	OKType PayloadType = iota
	ErrType
	AuthType
	AuthFailType
	AuthOKType
	LogType
	LogQueryOpts
	LogQueryResults
)

// SocketPayload represents the basic structure of all websocket requests. Decoding is a 2 stage
// process as we must first know the payload_type before we can decode the Data value into the appropriate
// struct.
type SocketPayload struct {
	PayloadType PayloadType     `json:"payload_type"`
	Data        json.RawMessage `json:"data"`
}

// SocketLogPayload contains individual log lines that are relayed
type SocketLogPayload struct {
	ServerName string `json:"server_name"`
	Message    string `json:"message"`
}

// socketState holds the global websocket session state and handlers
type socketState struct {
	*sync.RWMutex
	ws         *melody.Melody
	db         store.Store
	logMsgChan chan LogPayload
	sessions   map[*melody.Session]*socketSession
}

// socketSession represents the state of a client connected via websockets
type socketSession struct {
	IsClient bool
	State    State
	Person   model.Person
	// Is log broadcasting enabled
	BroadcastLog        bool
	LogQueryOpts        model.LogQueryOpts
	LogQueryOptsUpdated bool
	ctx                 context.Context
	eventChan           chan model.ServerEvent
	session             *melody.Session
	sendQ               chan []byte
	recvQ               chan []byte
}

func (s *socketSession) Log() *log.Entry {
	return log.WithFields(log.Fields{"addr": s.session.Request.RemoteAddr, "is_client": s.IsClient})
}

func (s *socketSession) send(b []byte) {
	select {
	case s.sendQ <- b:
		break
	default:
		s.Log().Errorf("send queue full")
	}
}

// EncodeWSPayload will return an encoded payload suitable for transmission over the wire
func EncodeWSPayload(t PayloadType, p interface{}) ([]byte, error) {
	b, e1 := json.Marshal(p)
	if e1 != nil {
		return nil, errors.Wrapf(e1, "failed to EncodeWSPayload base payload")
	}
	f, e2 := json.Marshal(SocketPayload{
		PayloadType: t,
		Data:        b,
	})
	if e2 != nil {
		return nil, errors.Wrapf(e1, "failed to EncodeWSPayload sub payload")
	}
	return f, nil
}

func (s *socketSession) writer() {
	for {
		select {
		case p := <-s.sendQ:
			if err := s.session.Write(p); err != nil {
				s.Log().Errorf("Failed to write payload over write: %v", err)
				continue
			}
		case <-s.ctx.Done():
			return
		}
	}
}

// reader sends out incoming log payloads to the client
func (s *socketSession) reader() {
	for {
		select {
		case r := <-s.recvQ:
			s.Log().Debugln(r)
		case e := <-s.eventChan:
			if !s.LogQueryOpts.ValidRecordType(e.EventType) {
				continue
			}
			// TODO
			b, err := EncodeWSPayload(LogType, e)
			if err != nil {
				s.Log().Errorf("Failed to EncodeWSPayload payload: %v", err)
				continue
			}
			if errE := s.session.Write(b); errE != nil {
				s.Log().Errorf("Failed to write to ws: %v", errE)
			}
		case <-s.ctx.Done():
			s.Log().Debugf("ws reader() shutdown")
			return
		}
	}
}

func (s *socketSession) setQueryOpts(opts model.LogQueryOpts) {
	s.LogQueryOpts = opts
	s.LogQueryOptsUpdated = true
}

func (s *socketSession) err(errType PayloadType, err error, args ...interface{}) {
	if len(args) == 1 {
		s.Log().Errorf(args[0].(string))
	} else if len(args) > 1 {
		s.Log().Errorf(args[0].(string), args[1:]...)
	}
	s.send(newWSErr(errType, err))
}

// newClientServiceState allocates and connects all websocket routes and session states
func newClientServiceState(logMsgChan chan LogPayload, db store.Store) *socketState {
	wsWeb := melody.New()
	wss := &socketState{
		RWMutex:    &sync.RWMutex{},
		ws:         wsWeb,
		db:         db,
		sessions:   map[*melody.Session]*socketSession{},
		logMsgChan: logMsgChan,
	}
	wsWeb.HandleMessage(wss.onMessage)
	wsWeb.HandleConnect(wss.onWSConnect)
	wsWeb.HandleDisconnect(wss.onWSDisconnect)
	wsWeb.HandleError(func(session *melody.Session, err error) {
		log.Errorf("WSERR: %v", err)
		// dc?
	})
	return wss
}

func (ws *socketState) onWSStart(c *gin.Context) {
	if err := ws.ws.HandleRequest(c.Writer, c.Request); err != nil {
		log.Errorf("Error handling ws request: %v", err)
	}
}

type SocketAuthReq struct {
	Token      string `json:"token"`
	IsServer   bool   `json:"is_server"`
	ServerName string `json:"server_name"`
}

type WebSocketAuthResp struct {
	Status  bool   `json:"status"`
	Message string `json:"message"`
}

type WSErrRes struct {
	Error string `json:"err"`
}

func newWSErr(errType PayloadType, err error) []byte {
	ev := ""
	if err != nil {
		ev = err.Error()
	}
	d, _ := json.Marshal(WSErrRes{Error: ev})
	b, _ := json.Marshal(SocketPayload{
		PayloadType: errType,
		Data:        d,
	})
	return b
}

func (ws *socketState) authenticateClient(ctx context.Context, req SocketAuthReq, s *socketSession) error {
	s.IsClient = true
	sid, err := sid64FromJWTToken(req.Token)
	if err != nil {
		return consts.ErrAuthentication
	}
	var p model.Person
	if errP := ws.db.GetPersonBySteamID(ctx, sid, &p); errP != nil || p.PermissionLevel < model.PModerator {
		return consts.ErrAuthentication
	}
	s.Person = p

	b, errEnc := EncodeWSPayload(AuthOKType, WebSocketAuthResp{
		Status:  true,
		Message: "Successfully authenticated",
	})
	if errEnc != nil {
		s.Log().Errorf("Failed to encode auth response payload: %v", errEnc)
		return consts.ErrAuthentication
	}
	if errW := s.session.Write(b); errW != nil {
		s.Log().Errorf("Failed to write client success response: %v", errW)
	}
	s.Log().Debugf("WS user authhenticated successfully")

	return nil
}

// onMessage handles incoming websocket payloads
// We always return authentication errors until the client is fully authed. This is to prevent
// any leaking of information to an attacker that can be further leveraged to aide in further
// attacks by this or other vectors.
func (ws *socketState) onMessage(session *melody.Session, msg []byte) {
	ws.Lock()
	defer ws.Unlock()
	sockSession, found := ws.sessions[session]
	if !found {
		log.Errorf("Unknown ws client sent message")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*300)
	defer cancel()

	var w SocketPayload
	if err := json.Unmarshal(msg, &w); err != nil {
		sockSession.err(ErrType, consts.ErrMalformedRequest, "Failed to unmarshal ws payload")
		return
	}

	switch sockSession.State {
	case AwaitingAuthentication:
		ws.onAwaitingAuthentication(ctx, &w, sockSession)
	case Authenticated:
		ws.onAuthenticatedPayload(ctx, &w, sockSession)
	default:
		log.Errorf("Unhandled session state: %v", sockSession.State)
	}
}

func (ws *socketState) onAwaitingAuthentication(ctx context.Context, w *SocketPayload, c *socketSession) {
	var req SocketAuthReq
	if err := json.Unmarshal(w.Data, &req); err != nil {
		c.err(AuthFailType, consts.ErrAuthentication, "Failed to unmarshal auth data")
		return
	}
	var e error
	e = ws.authenticateClient(ctx, req, c)
	if e != nil {
		c.err(AuthFailType, e)
		return
	}
	c.State = Authenticated
}

func (ws *socketState) onAuthenticatedPayload(_ context.Context, w *SocketPayload, c *socketSession) {
	switch w.PayloadType {
	case LogType:
		var l LogPayload
		if err := json.Unmarshal(w.Data, &l); err != nil {
			c.err(ErrType, consts.ErrMalformedRequest, "Failed to unmarshal logpayload data")
			return
		}
		ws.logMsgChan <- l
	case LogQueryOpts:
		var opts model.LogQueryOpts
		if err := json.Unmarshal(w.Data, &opts); err != nil {
			c.err(ErrType, consts.ErrMalformedRequest, "Failed to unmarshal query data")
			return
		}
		c.setQueryOpts(opts)
		c.Log().Debugf("Updated query opts: %v", opts)
		go func() {
			results, err := ws.db.FindLogEvents(c.ctx, opts)
			if err != nil {
				c.Log().Errorf("Error sending pre-cache to client")
				return
			}
			for _, r := range results {
				b, e := EncodeWSPayload(LogQueryResults, r)
				if e != nil {
					c.Log().Errorf("Failed to encode payload: %v", e)
					return
				}
				c.send(b)
			}
		}()
	default:
		c.Log().Debugf("Unhandled payload: %v", w)
	}
}

// onWSConnect sets up the websocket client in the session list and registers it to receive all log events
// by default.
func (ws *socketState) onWSConnect(session *melody.Session) {
	client := &socketSession{
		State:     Closed,
		ctx:       context.Background(),
		eventChan: make(chan model.ServerEvent),
		session:   session,
		sendQ:     make(chan []byte, sendQueueSize),
		recvQ:     make(chan []byte),
	}
	go client.reader()
	go client.writer()
	client.State = AwaitingAuthentication
	ws.Lock()
	ws.sessions[session] = client
	ws.Unlock()
	client.Log().Infof("WS client connect")

}

// onWSDisconnect will remove the client from the active session list and unregister itself
// from the event broadcasts
func (ws *socketState) onWSDisconnect(session *melody.Session) {
	ws.Lock()
	defer ws.Unlock()
	c, found := ws.sessions[session]
	if !found {
		log.Errorf("Unregistered ws client")
		return
	}
	c.State = Closing
	delete(ws.sessions, session)
	log.WithField("addr", session.Request.RemoteAddr).Infof("WS client disconnect")
	if err := event.UnregisterConsumer(c.eventChan); err != nil {
		log.Errorf("Failed to unregister event consumer")
	}
	// TODO cleanup remaining queues
	c.State = Closed

}
