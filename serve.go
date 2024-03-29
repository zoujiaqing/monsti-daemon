package main

import (
	"bytes"
	"fmt"
	"github.com/gorilla/context"
	"github.com/gorilla/sessions"
	"github.com/monsti/monsti-daemon/worker"
	"github.com/monsti/rpc/client"
	"github.com/monsti/util/l10n"
	"github.com/monsti/util/template"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

// nodeHandler is a net/http handler to process incoming HTTP requests.
type nodeHandler struct {
	Renderer template.Renderer
	Settings *settings
	// Hosts is a map from hosts to site names.
	Hosts      map[string]string
	NodeQueues map[string]chan worker.Ticket
	// Log is the logger used by the node handler.
	Log *log.Logger
}

// QueueTicket adds a ticket to the ticket queue of the corresponding
// node type (ticket.Node.Type).
func (h *nodeHandler) QueueTicket(ticket worker.Ticket) {
	nodeType := ticket.Node.Type
	if _, ok := h.NodeQueues[nodeType]; !ok {
		panic("Missing queue for node type " + nodeType)
	}
	h.NodeQueues[nodeType] <- ticket
}

// splitAction splits and returns the path and @@action of the given URL.
func splitAction(path string) (string, string) {
	tokens := strings.Split(path, "/")
	last := tokens[len(tokens)-1]
	var action string
	if len(last) > 2 && last[:2] == "@@" {
		action = last[2:]
	}
	nodePath := path
	if len(action) > 0 {
		nodePath = path[:len(path)-(len(action)+3)]
		if len(nodePath) == 0 {
			nodePath = "/"
		}
	}
	return nodePath, action
}

// ServeHTTP handles incoming HTTP requests.
func (h *nodeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			var buf bytes.Buffer
			fmt.Fprintf(&buf, "panic: %v\n", err)
			buf.Write(debug.Stack())
			h.Log.Println(buf.String())
			http.Error(w, "Application error.",
				http.StatusInternalServerError)
		}
	}()
	nodePath, action := splitAction(r.URL.Path)
	if len(action) == 0 && nodePath[len(nodePath)-1] != '/' {
		newPath, err := url.Parse(nodePath + "/")
		if err != nil {
			panic("Could not parse request URL:" + err.Error())
		}
		url := r.URL.ResolveReference(newPath)
		http.Redirect(w, r, url.String(), http.StatusSeeOther)
		return
	}
	site_name, ok := h.Hosts[r.Host]
	if !ok {
		panic("No site found for host " + r.Host)
	}
	site := h.Settings.Sites[site_name]
	site.Name = site_name
	session := getSession(r, site)
	defer context.Clear(r)
	cSession := getClientSession(session, site.Directories.Config)
	cSession.Locale = site.Locale
	node, err := lookupNode(site.Directories.Data, nodePath)
	if err != nil {
		h.Log.Println("Node not found.")
		http.Error(w, "Node not found: "+err.Error(), http.StatusNotFound)
		return
	}

	if !checkPermission(action, cSession) {
		http.Error(w, "Unauthorized.", http.StatusUnauthorized)
		return
	}
	switch action {
	case "login":
		h.Login(w, r, node, session, cSession, site)
	case "logout":
		h.Logout(w, r, node, session)
	case "add":
		h.Add(w, r, node, session, cSession, site)
	case "remove":
		h.Remove(w, r, node, session, cSession, site)
	default:
		h.RequestNode(w, r, node, action, session, cSession, site)
	}
}

// RequestNode handles node requests.
func (h *nodeHandler) RequestNode(w http.ResponseWriter, r *http.Request,
	node client.Node, action string, session *sessions.Session,
	cSession *client.Session, site site) {
	// Setup ticket and send to workers.
	h.Log.Println(site.Name, r.Method, r.URL.Path)
	c := make(chan client.Response)
	h.QueueTicket(worker.Ticket{
		Node:         node,
		Request:      r,
		ResponseChan: c,
		Session:      *cSession,
		Action:       action,
		Site:         site.Name})

	// Process response received from a worker.
	// If the worker process dies, the channel will be closed.
	res := <-c
	h.ProcessNodeResponse(res, w, r, node, action, session,
		cSession, site)
}

func (h *nodeHandler) ProcessNodeResponse(res client.Response,
	w http.ResponseWriter, r *http.Request, node client.Node,
	action string, session *sessions.Session,
	cSession *client.Session, site site) {
	G := l10n.UseCatalog(cSession.Locale)
	if len(res.Body) == 0 && len(res.Redirect) == 0 {
		http.Error(w, "Application error.",
			http.StatusInternalServerError)
		return
	}
	if res.Node != nil {
		oldPath := node.Path
		node = *res.Node
		node.Path = oldPath
	}
	if len(res.Redirect) > 0 {
		http.Redirect(w, r, res.Redirect, http.StatusSeeOther)
		return
	}
	env := masterTmplEnv{Node: node, Session: cSession}
	if action == "edit" {
		env.Title = fmt.Sprintf(G("Edit \"%s\""), node.Title)
		env.Flags = EDIT_VIEW
	}
	var content []byte
	if res.Raw {
		content = res.Body
	} else {
		content = []byte(renderInMaster(h.Renderer, res.Body, env, h.Settings,
			site, cSession.Locale))
	}
	err := session.Save(r, w)
	if err != nil {
		panic(err.Error())
	}
	w.Write(content)
}

// AddNodeProcess starts a worker process to handle the given node type.
func (h *nodeHandler) AddNodeProcess(nodeType string, logger *log.Logger) {
	if _, ok := h.NodeQueues[nodeType]; !ok {
		h.NodeQueues[nodeType] = make(chan worker.Ticket)
	}
	nodeRPC := NodeRPC{Settings: h.Settings, Log: logger}
	worker := worker.NewWorker("monsti-"+nodeType, h.NodeQueues[nodeType],
		&nodeRPC, h.Settings.Directories.Config, h.Log)
	nodeRPC.Worker = worker
	callback := func() {
		log.Println("Trying to restart worker in 5 seconds.")
		time.Sleep(5 * time.Second)
		h.AddNodeProcess(nodeType, h.Log)
	}
	if err := worker.Run(callback); err != nil {
		panic("Could not run worker: " + err.Error())
	}
}
