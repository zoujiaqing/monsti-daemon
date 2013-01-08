package main

import (
	"bytes"
	"github.com/gorilla/sessions"
	"github.com/monsti/monsti-daemon/worker"
	"github.com/monsti/rpc/client"
	"github.com/monsti/rpc/types"
	utesting "github.com/monsti/util/testing"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// setupRPC creates a RPC environment for testing.
func setupRPC(t *testing.T, testName string) (NodeRPC, string, func()) {
	root, cleanup, err := utesting.CreateDirectoryTree(map[string]string{
		"/foo/node.yaml": `
type: document
title: FooNode
created: "02 Jan 06 15:04 UTC"
createdby: FooUser
lastupdate: "03 Jan 06 10:03 UTC"
lastupdateby: FooUsersMom`}, testName)
	if err != nil {
		t.Fatalf("Could not create directory tree: ", err)
	}
	site_ := site{Name: "FooSite"}
	site_.Directories.Data = root
	settings := settings{Sites: map[string]site{site_.Name: site_}}
	csession := client.Session{User: &client.User{Login: "BarUser"}}
	ticket := worker.Ticket{Site: site_.Name, Session: csession}
	worker := worker.Worker{Ticket: &ticket}
	session := sessions.Session{}
	return NodeRPC{&worker, settings, &session, nil}, root, cleanup
}

func TestRPCWriteNodeData(t *testing.T) {
	rpc, root, cleanup := setupRPC(t, "TestRPCWriteNodeData")
	defer cleanup()
	var reply int
	err := rpc.WriteNodeData(&types.WriteNodeDataArgs{
		Path: "/foo", File: "test.txt", Content: "Hey World!"}, &reply)
	if err != nil {
		t.Fatalf("Could not call WriteNodeData: ", err)
	}
	writtenData, err := ioutil.ReadFile(filepath.Join(root, "/foo/test.txt"))
	if err != nil {
		t.Errorf("Could not read file which should have been written: ", err)
	} else if !bytes.Equal(writtenData, []byte("Hey World!")) {
		t.Errorf("Written data is %q, should be \"Hey World!\"", writtenData)
	}
	node, err := lookupNode(root, "/foo")
	if err != nil {
		t.Errorf("Node not found/parsable: ", err)
	}
	created1, _ := time.Parse(time.RFC822, "02 Jan 06 15:04 UTC")
	created := client.Time{&created1}
	updated1, _ := time.Parse(time.RFC822, "02 Jan 06 15:04 UTC")
	updated := client.Time{&updated1}
	expected := client.Node{
		Type: "Document", Title: "FooNode",
		Created:      &created,
		CreatedBy:    "FooUser",
		LastUpdate:   &updated,
		LastUpdateBy: "BarUser"}
	if !reflect.DeepEqual(node, expected) {
		t.Errorf("Updated node is %v, should be %v", node, expected)
	}
}
