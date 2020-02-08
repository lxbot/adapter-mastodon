package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"github.com/k3a/html2text"
	"github.com/lxbot/lxlib"
	"github.com/mattn/go-mastodon"
	"log"
	"os"
	"strings"
	"time"
)

type M = map[string]interface{}

var ch *chan M
var client *mastodon.Client
var me *mastodon.Account

func Boot(c *chan M) {
	ch = c
	gob.Register(mastodon.Status{})
	url := os.Getenv("LXBOT_MASTODON_BASE_URL")
	if url == "" {
		log.Fatalln("invalid url:", "'LXBOT_MASTODON_BASE_URL' にAPI URLを設定してください")
	}
	token := os.Getenv("LXBOT_MASTODON_ACCESS_TOKEN")
	if token == "" {
		log.Fatalln("invalid token:", "'LXBOT_MASTODON_ACCESS_TOKEN' にアクセストークンを設定してください")
	}
	client = mastodon.NewClient(&mastodon.Config{
		Server:      url,
		AccessToken: token,
	})

	account, err := client.GetAccountCurrentUser(context.TODO())
	if err != nil {
		log.Fatalln("account fetch error:", err)
	}
	me = account

	ws := client.NewWSClient()
	go connect(ws)
}

func Send(msg M) {
	m, err := lxlib.NewLXMessage(msg)
	if err != nil {
		log.Println(err)
		return
	}

	texts := split(m.Message.Text, 400)
	inReplyToID := mastodon.ID("")
	if msg["is_reply"] != nil && msg["is_reply"].(bool) {
		inReplyToID = mastodon.ID(m.Message.ID)
	}
	for _, v := range texts {
		status, err := client.PostStatus(context.TODO(), &mastodon.Toot{
			Status:      v,
			InReplyToID: inReplyToID,
			Visibility:  "unlisted",
		})
		if err != nil {
			log.Println(err)
			return
		}
		inReplyToID = status.ID
	}
}

func Reply(msg M) {
	m, err := lxlib.NewLXMessage(msg)
	if err != nil {
		log.Println(err)
		return
	}

	user := m.User.ID
	texts := split(m.Message.Text, 400)
	inReplyToID := mastodon.ID(m.Message.ID)
	for _, v := range texts {
		status, err := client.PostStatus(context.TODO(), &mastodon.Toot{
			Status:      "@" + user + " " + v,
			InReplyToID: inReplyToID,
			Visibility:  "unlisted",
		})
		if err != nil {
			log.Println(err)
			return
		}
		inReplyToID = status.ID
	}
}

func connect(client *mastodon.WSClient) {
	event, err := client.StreamingWSUser(context.Background())
	if err != nil {
		log.Println(err)
		time.Sleep(10 * time.Second)
		go connect(client)
		return
	}

	log.Println("start streaming loop")
LOOP:
	for {
		e := <-event
		switch e.(type) {
		case *mastodon.UpdateEvent:
			ue := e.(*mastodon.UpdateEvent)
			onUpdate(ue.Status)
			break
		case *mastodon.ErrorEvent:
			break LOOP
		}
	}
	log.Println("exits streaming loop")

	go connect(client)
}

func onUpdate(status *mastodon.Status) {
	isReply := false
	text := html2text.HTML2Text(status.Content)
	for _, v := range status.Mentions {
		text = strings.ReplaceAll(text, me.URL, "")
		if v.Acct == me.Acct {
			isReply = true
		}
	}
	text = strings.TrimSpace(text)

	attachments := make([]M, len(status.MediaAttachments))
	for i, v := range status.MediaAttachments {
		attachments[i] = M{
			"url": v.URL,
			"description": v.Description,
		}
	}

	*ch <- M{
		"user": M{
			"id":   status.Account.Acct,
			"name": status.Account.DisplayName,
		},
		"room": M{
			"id":          "mastodon",
			"name":        "mastodon",
			"description": "mastodon",
		},
		"message": M{
			"id":          string(status.ID),
			"text":        text,
			"attachments": attachments,
		},
		"is_reply": isReply,
		"raw": status,
	}
}

func split(s string, n int) []string {
	result := make([]string, 0)
	runes := bytes.Runes([]byte(s))
	tmp := ""
	for i, r := range runes {
		tmp = tmp + string(r)
		if (i + 1) % n == 0 {
			result = append(result, tmp)
			tmp = ""
		}
	}
	return append(result, tmp)
}