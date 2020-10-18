package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"github.com/PuerkitoBio/goquery"
	"github.com/lxbot/lxlib"
	"github.com/mattn/go-mastodon"
	"log"
	"net/url"
	"os"
	"strings"
	"time"
)

type (
	M = map[string]interface{}
	EventType int
)

const (
	UpdateEvent EventType = iota + 1
	NotificationEvent
)

var ch *chan M
var client *mastodon.Client
var me *mastodon.Account
var allowList []string
var fullAcct string

func Boot(c *chan M) {
	ch = c
	gob.Register(mastodon.Status{})
	al := os.Getenv("LXBOT_MASTODON_ALLOW_LIST")
	if al != "" {
		allowList = strings.Split(al, ",")
		for i, acct := range allowList {
			allowList[i] = strings.TrimSpace(acct)
			log.Println("allow list: " + allowList[i])
		}
	}
	u := os.Getenv("LXBOT_MASTODON_BASE_URL")
	if u == "" {
		log.Fatalln("invalid url:", "'LXBOT_MASTODON_BASE_URL' にAPI URLを設定してください")
	}
	token := os.Getenv("LXBOT_MASTODON_ACCESS_TOKEN")
	if token == "" {
		log.Fatalln("invalid token:", "'LXBOT_MASTODON_ACCESS_TOKEN' にアクセストークンを設定してください")
	}
	client = mastodon.NewClient(&mastodon.Config{
		Server:      u,
		AccessToken: token,
	})

	account, err := client.GetAccountCurrentUser(context.TODO())
	if err != nil {
		log.Fatalln("account fetch error:", err)
	}
	me = account

	up, err := url.Parse(u)
	if err != nil {
		log.Fatalln("url parse error:", err)
	}
	fullAcct = "@" + me.Acct + "@" + up.Host

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
		log.Println("onEvent: ", e)
		switch e.(type) {
		case *mastodon.UpdateEvent:
			ue := e.(*mastodon.UpdateEvent)
			log.Println("onUpdateEvent: ", ue)
			onUpdate(UpdateEvent, ue.Status)
			break
		case *mastodon.NotificationEvent:
			ne := e.(*mastodon.NotificationEvent)
			log.Println("onNotificationEvent: ", ne)
			onUpdate(NotificationEvent, ne.Notification.Status)
			break
		case *mastodon.ErrorEvent:
			break LOOP
		}
	}
	log.Println("exits streaming loop")

	go connect(client)
}

func onUpdate(event EventType, status *mastodon.Status) {
	if status.Account.Acct == me.Acct {
		log.Println("own message, skipped")
		return
	}

	isReply := false
	text := html2text(status.Content)
	for _, v := range status.Mentions {
		text = strings.ReplaceAll(text, me.URL, "")
		text = strings.ReplaceAll(text, fullAcct, "")
		text = strings.ReplaceAll(text, "@" + me.Acct, "")
		if v.Acct == me.Acct {
			isReply = true
		}
	}
	if event == UpdateEvent && isReply {
		log.Println("this event should be process with NotificationEvent")
		return
	}

	text = strings.TrimSpace(text)
	log.Println(text)

	if len(allowList) != 0 {
		for _, acct := range allowList {
			if acct == status.Account.Acct {
				log.Println("allow: ", status.Account.Acct)
				goto PROCESS
			}
		}
		log.Println("deny: ", status.Account.Acct)
		if isReply {
			Reply(M{
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
					"text":        "このbotは許可リストが設定されています。あなたのアカウントは許可リストに含まれていません。",
					"attachments": []M{},
				},
				"is_reply": isReply,
				"raw": status,
			})
		}
		return
	}

PROCESS:
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

func html2text(s string) string {
	log.Println("raw:", s)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(s))
	if err != nil {
		log.Println(err)
		return s
	}
	doc.Find("br").Each(func(i int, selection *goquery.Selection) {
		selection.SetText("\n")
	})
	text := doc.Text()
	log.Println("html2text:", text)
	return text
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