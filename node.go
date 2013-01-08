package main

import (
	"fmt"
	"github.com/gorilla/sessions"
	"github.com/monsti/form"
	"github.com/monsti/rpc/client"
	"github.com/monsti/util/l10n"
	"github.com/monsti/util/template"
	"io/ioutil"
	"launchpad.net/goyaml"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// getFooter retrieves the footer.
//
// root is the path to the data directory
//
// Returns an empty string if there is no footer.
func getFooter(root string) string {
	path := filepath.Join(root, "footer.html")
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

// getBelowHeader retrieves the below header content for the given node.
//
// path is the node's path.
// root is the path to the data directory.
//
// Returns an empty string if there is no below header content.
func getBelowHeader(path, root string) string {
	file := filepath.Join(root, path, "below_header.html")
	content, err := ioutil.ReadFile(file)
	if err != nil {
		return ""
	}
	return string(content)
}

// getSidebar retrieves the sidebar content for the given node.
//
// path is the node's path.
// root is the path to the data directory.
//
// It traverses up to the root until it finds a node with defined sidebar
// content.
//
// Returns an empty string if there is no sidebar content.
func getSidebar(path, root string) string {
	for {
		file := filepath.Join(root, path, "sidebar.html")
		content, err := ioutil.ReadFile(file)
		if err != nil {
			if path == filepath.Dir(path) {
				break
			}
			path = filepath.Dir(path)
			continue
		}
		return string(content)
	}
	return ""
}

// navLink represents a link in the navigation.
type navLink struct {
	Name, Target string
	Active       bool "active,omitempty"
}

type navigation []navLink

// getNav returns the navigation for the given node.
// 
// nodePath is the path of the node for which to get the navigation.
// active is the currently active node.
// root is the path of the data directory.
//
// The keys of the returned map are the link titles, the values are
// the link targets.
//
// If the given node has no navigation (i.e. no navigation.yaml) and recursive
// is true, search recursively up but excluding the root for a navigation. If
// recursive is false, getNav returns nil for this case.
//
// The second return value is set to the path of the node for which a navigation
// was found via a recursive search. In all other cases, it's an empty string.
func getNav(nodePath, active string, recursive bool, root string) (navLinks navigation,
	navRoot string) {
	var content []byte
	hasNav := true
	for {
		file := filepath.Join(root, nodePath, "navigation.yaml")
		var err error
		content, err = ioutil.ReadFile(file)
		if err != nil {
			hasNav = false
			nodePath = filepath.Dir(nodePath)
			if !recursive || nodePath == filepath.Dir(nodePath) {
				break
			}
			continue
		}
		if recursive {
			navRoot = nodePath
		}
		break
	}
	goyaml.Unmarshal(content, &navLinks)
	for i, link := range navLinks {
		if link.Target == active {
			navLinks[i].Active = true
			break
		}
	}
	if len(navLinks) == 0 && hasNav {
		navLinks = navigation{}
		return
	}
	return
}

// dumpNav unmarshals the navigation and writes it to the given node directory.
func (nav navigation) Dump(nodePath, root string) {
	for i := range nav {
		nav[i].Active = false
	}
	content, err := goyaml.Marshal(&nav)
	if err != nil {
		panic("Could not marshal navigation: " + err.Error())
	}
	path := filepath.Join(root, nodePath[1:], "navigation.yaml")
	err = ioutil.WriteFile(path, content, 0600)
	if err != nil {
		panic("Could not write navigation: " + err.Error())
	}
}

// MakeAbsolute converts relative targets to absolute ones by adding the given
// root path.
func (nav *navigation) MakeAbsolute(root string) {
	for i := range *nav {
		if (*nav)[i].Target[0] != '/' {
			(*nav)[i].Target = path.Join(root, (*nav)[i].Target)
		}
	}
}

// Add adds a link with the given name and target to the navigation.
func (nav *navigation) Add(name, target string) {
	*nav = append(*nav, navLink{Name: name, Target: target})
}

// Remove removes all links with the given target from the navigation.
func (nav *navigation) Remove(target string) {
	ret := make(navigation, 0, len(*nav)-1)
	for _, v := range *nav {
		if v.Target != target {
			ret = append(ret, v)
		}
	}
	*nav = ret
}

type addFormData struct {
	Type, Name, Title string
}

// Add handles add requests.
func (h *nodeHandler) Add(w http.ResponseWriter, r *http.Request,
	node client.Node, session *sessions.Session, cSession *client.Session,
	site site) {
	G := l10n.UseCatalog(cSession.Locale)
	data := addFormData{}
	nodeTypeOptions := []form.Option{}
	for _, nodeType := range h.Settings.NodeTypes {
		nodeTypeOptions = append(nodeTypeOptions,
			form.Option{nodeType, nodeType})
	}
	selectWidget := form.SelectWidget{nodeTypeOptions}
	form := form.NewForm(&data, form.Fields{
		"Type": form.Field{G("Content type"), "", form.Required(G("Required.")), selectWidget},
		"Name": form.Field{G("Name"),
			G("The name as it should appear in the URL."),
			form.And(form.Required(G("Required.")), form.Regex(`^[-\w]*$`,
				G("Contains	invalid characters."))), nil},
		"Title": form.Field{G("Title"), "", form.Required(G("Required.")), nil}})
	switch r.Method {
	case "GET":
	case "POST":
		r.ParseForm()
		if form.Fill(r.Form) {
			data.Name = strings.ToLower(data.Name)
			if !inStringSlice(data.Type, h.Settings.NodeTypes) {
				panic("Can't add this content type.")
			}
			newPath := filepath.Join(node.Path, data.Name)
			newNode := client.Node{
				Path:  newPath,
				Type:  data.Type,
				Title: data.Title}
			if err := writeNode(newNode, site.Directories.Data); err != nil {
				panic("Can't add node: " + err.Error())
			}
			nav, _ := getNav(node.Path, "", false, site.Directories.Data)
			nav.Add(data.Title, data.Name)
			nav.Dump(node.Path, site.Directories.Data)
			http.Redirect(w, r, newPath+"/@@edit", http.StatusSeeOther)
			return
		}
	default:
		panic("Request method not supported: " + r.Method)
	}
	body := h.Renderer.Render("daemon/actions/addform", template.Context{
		"Form": form.RenderData()}, cSession.Locale, site.Directories.Templates)
	env := masterTmplEnv{Node: node, Session: cSession,
		Flags: EDIT_VIEW, Title: G("Add content")}
	fmt.Fprint(w, renderInMaster(h.Renderer, []byte(body), env, h.Settings,
		site, cSession.Locale))
}

type removeFormData struct {
	Confirm int
}

// Remove handles remove requests.
func (h *nodeHandler) Remove(w http.ResponseWriter, r *http.Request,
	node client.Node, session *sessions.Session, cSession *client.Session,
	site site) {
	G := l10n.UseCatalog(cSession.Locale)
	data := removeFormData{}
	form := form.NewForm(&data, form.Fields{
		"Confirm": form.Field{G("Confirm"), "", form.Required(G("Required.")),
			new(form.HiddenWidget)}})
	switch r.Method {
	case "GET":
	case "POST":
		r.ParseForm()
		if form.Fill(r.Form) {
			removeNode(node.Path, site.Directories.Data)
			http.Redirect(w, r, path.Dir(node.Path), http.StatusSeeOther)
			return
		}
	default:
		panic("Request method not supported: " + r.Method)
	}
	data.Confirm = 1489
	body := h.Renderer.Render("daemon/actions/removeform", template.Context{
		"Form": form.RenderData(), "Node": node},
		cSession.Locale, site.Directories.Templates)
	env := masterTmplEnv{Node: node, Session: cSession,
		Flags: EDIT_VIEW, Title: fmt.Sprintf(G("Remove \"%v\""), node.Title)}
	fmt.Fprint(w, renderInMaster(h.Renderer, []byte(body), env, h.Settings,
		site, cSession.Locale))
}

// lookupNode look ups a node at the given path.
// If no such node exists, return nil.
func lookupNode(root, path string) (client.Node, error) {
	node_path := filepath.Join(root, path[1:], "node.yaml")
	content, err := ioutil.ReadFile(node_path)
	if err != nil {
		return client.Node{}, err
	}
	var node client.Node
	log.Printf("Unmarshall %s", content)
	if err = goyaml.Unmarshal(content, &node); err != nil {
		log.Printf("got error", err)
		return client.Node{}, err
	}
	log.Printf("got", node)
	node.Path = path
	return node, nil
}

// writeNode writes the given node to the data directory located at the given
// root.
func writeNode(node client.Node, root string) error {
	path := node.Path
	node.Path = ""
	content, err := goyaml.Marshal(&node)
	if err != nil {
		return err
	}
	node_path := filepath.Join(root, path[1:],
		"node.yaml")
	if err := os.Mkdir(filepath.Dir(node_path), 0700); err != nil {
		if !os.IsExist(err) {
			panic("Can't create directory for new node: " + err.Error())
		}
	}
	return ioutil.WriteFile(node_path, content, 0600)
}

// removeNode recursively removes the given node from the data directory located
// at the given root and from the navigation of the parent node.
func removeNode(path, root string) {
	nodePath := filepath.Join(root, path[1:])
	parent := filepath.Dir(path)
	if parent != path {
		nav, _ := getNav(parent, "", false, root)
		nav.Remove(filepath.Base(path))
		nav.Dump(parent, root)
	}
	if err := os.RemoveAll(nodePath); err != nil {
		panic("Can't remove node: " + err.Error())
	}
}
