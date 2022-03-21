package main

import (
	"bytes"
	"context"
	"encoding/gob"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/lxbot/lxlib/v2"
	"github.com/lxbot/lxlib/v2/common"
	"github.com/lxbot/lxlib/v2/lxtypes"
	"github.com/mattn/go-mastodon"
	"github.com/mitchellh/mapstructure"
)

type (
	EventType int
)

const (
	UpdateEvent EventType = iota + 1
	NotificationEvent
)

var adapter *lxlib.Adapter
var messageCh *chan *lxtypes.Message
var client *mastodon.Client
var me *mastodon.Account
var allowList []string
var fullAcct string

func main() {
	adapter, messageCh = lxlib.NewAdapter()

	initialize()
	listen()
}

func initialize() {
	gob.Register(mastodon.Status{})
	al := os.Getenv("LXBOT_MASTODON_ALLOW_LIST")
	if al != "" {
		allowList = strings.Split(al, ",")
		for i, acct := range allowList {
			allowList[i] = strings.TrimSpace(acct)
			common.InfoLog("allow list: " + allowList[i])
		}
	}
	u := os.Getenv("LXBOT_MASTODON_BASE_URL")
	if u == "" {
		common.FatalLog("invalid url:", "'LXBOT_MASTODON_BASE_URL' にAPI URLを設定してください")
	}
	token := os.Getenv("LXBOT_MASTODON_ACCESS_TOKEN")
	if token == "" {
		common.FatalLog("invalid token:", "'LXBOT_MASTODON_ACCESS_TOKEN' にアクセストークンを設定してください")
	}
	client = mastodon.NewClient(&mastodon.Config{
		Server:      u,
		AccessToken: token,
	})

	account, err := client.GetAccountCurrentUser(context.TODO())
	if err != nil {
		common.FatalLog("account fetch error:", err)
	}
	me = account

	up, err := url.Parse(u)
	if err != nil {
		common.FatalLog("url parse error:", err)
	}
	fullAcct = "@" + me.Acct + "@" + up.Host

	ws := client.NewWSClient()
	go connect(ws)
}

func listen() {
	for {
		message := <-*messageCh
		go send(message)
	}
}

func send(message *lxtypes.Message) {
	prefix := ""
	inReplyToID := mastodon.ID("")
	common.DebugLog("mode:", message.Mode)
	if message.Mode == lxtypes.ReplyMode {
		inReplyToID = mastodon.ID(message.Room.ID)
		prefix = "@" + message.User.ID + " "
		common.DebugLog("prefix:", prefix)
	}

	visibility := "unlisted"
	if message.Raw != nil {
		raw := message.Raw.(map[string]interface{})
		status := new(mastodon.Status)
		if err := mapstructure.WeakDecode(raw, status); err != nil {
			common.ErrorLog(err)
			return
		}
		if status.Visibility != "public" {
			visibility = status.Visibility
		}
	}

	for _, content := range message.Contents {
		texts := split(content.Text, 400)

		for _, text := range texts {
			body := prefix + text
			toot := &mastodon.Toot{
				Status:      body,
				InReplyToID: inReplyToID,
				Visibility:  visibility,
			}
			common.DebugLog("visibility:", visibility, "toot:", body)
			status, err := client.PostStatus(context.TODO(), toot)
			if err != nil {
				common.ErrorLog(err)
				return
			}
			common.DebugLog("status:", status)
			inReplyToID = status.ID
		}
	}
}

func connect(client *mastodon.WSClient) {
	event, err := client.StreamingWSUser(context.Background())
	if err != nil {
		common.ErrorLog(err)
		time.Sleep(10 * time.Second)
		go connect(client)
		return
	}

	common.InfoLog("start streaming loop")
LOOP:
	for {
		e := <-event
		common.InfoLog("onEvent: ", e)
		switch e.(type) {
		case *mastodon.UpdateEvent:
			ue := e.(*mastodon.UpdateEvent)
			common.InfoLog("onUpdateEvent: ", ue)
			onUpdate(UpdateEvent, ue.Status)
			break
		case *mastodon.NotificationEvent:
			ne := e.(*mastodon.NotificationEvent)
			common.InfoLog("onNotificationEvent: ", ne)
			onUpdate(NotificationEvent, ne.Notification.Status)
			break
		case *mastodon.ErrorEvent:
			break LOOP
		}
	}
	common.InfoLog("exits streaming loop")

	go connect(client)
}

func onUpdate(event EventType, status *mastodon.Status) {
	if status.Account.Acct == me.Acct {
		common.InfoLog("own message, skipped")
		return
	}

	isReply := false
	text := html2text(status.Content)
	for _, v := range status.Mentions {
		text = strings.ReplaceAll(text, me.URL, "")
		text = strings.ReplaceAll(text, fullAcct, "")
		text = strings.ReplaceAll(text, "@"+me.Acct, "")
		if v.Acct == me.Acct {
			isReply = true
		}
	}
	if event == UpdateEvent && isReply {
		common.InfoLog("this event should be process with NotificationEvent")
		return
	}

	if len(allowList) != 0 {
		for _, acct := range allowList {
			if acct == status.Account.Acct {
				common.InfoLog("allow: ", status.Account.Acct)
				goto PROCESS
			}
		}
		common.WarnLog("deny: ", status.Account.Acct)
		if isReply {
			send(&lxtypes.Message{
				User: lxtypes.User{
					ID:   status.Account.Acct,
					Name: status.Account.DisplayName,
				},
				Room: lxtypes.Room{
					ID:          string(status.ID),
					Name:        "mastodon",
					Description: "mastodon",
				},
				Contents: []lxtypes.Content{
					{
						ID:          string(status.ID),
						Text:        "このbotは許可リストが設定されています。あなたのアカウントは許可リストに含まれていません。",
						Attachments: make([]lxtypes.Attachment, 0),
					},
				},
				Mode: lxtypes.ReplyMode,
				Raw:  nil,
			})
		}
		return
	}

PROCESS:
	attachments := make([]lxtypes.Attachment, len(status.MediaAttachments))
	for i, v := range status.MediaAttachments {
		attachments[i] = lxtypes.Attachment{
			Url:         v.URL,
			Description: v.Description,
		}
	}

	adapter.Send(&lxtypes.Message{
		User: lxtypes.User{
			ID:   status.Account.Acct,
			Name: status.Account.DisplayName,
		},
		Room: lxtypes.Room{
			ID:          string(status.ID),
			Name:        "mastodon",
			Description: "mastodon",
		},
		Contents: []lxtypes.Content{
			{
				ID:          string(status.ID),
				Text:        strings.TrimSpace(text),
				Attachments: attachments,
			},
		},
		Raw: status,
	})
}

func html2text(s string) string {
	common.InfoLog("raw:", s)
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(s))
	if err != nil {
		common.ErrorLog(err)
		return s
	}
	doc.Find("br").Each(func(i int, selection *goquery.Selection) {
		selection.SetText("\n")
	})
	text := doc.Text()
	common.InfoLog("html2text:", text)
	return text
}

func split(s string, n int) []string {
	result := make([]string, 0)
	runes := bytes.Runes([]byte(s))
	tmp := ""
	for i, r := range runes {
		tmp = tmp + string(r)
		if (i+1)%n == 0 {
			result = append(result, tmp)
			tmp = ""
		}
	}
	return append(result, tmp)
}
