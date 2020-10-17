package main

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type slackListener struct {
	Token             string
	VerificationToken string
	client            *slack.Client
	EmojiName         string
	EventDestination  chan<- *slackevents.ReactionAddedEvent
	myUserID          string
}

func newSlackListener(slackToken string, queue chan<- *slackevents.ReactionAddedEvent) *slackListener {
	sl := slackListener{
		Token:            slackToken,
		client:           slack.New(slackToken),
		EventDestination: queue,
	}
	myProfile, err := sl.client.AuthTest()
	if err != nil {
		log.Fatal(errors.Wrap(err, "Running initial auth test"))
	}
	sl.myUserID = myProfile.UserID
	return &sl
}

func (sl *slackListener) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (sl *slackListener) handler(w http.ResponseWriter, r *http.Request) {
	buf := new(bytes.Buffer)
	buf.ReadFrom(r.Body)
	body := buf.String()
	parseOpts := []slackevents.Option{}
	if sl.VerificationToken != "" {
		log.Trace("Using verification token")
		parseOpts = append(parseOpts, slackevents.OptionVerifyToken(&slackevents.TokenComparator{VerificationToken: sl.VerificationToken}))
	}
	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), parseOpts...)

	if err != nil {
		log.Error(errors.Wrap(err, "Parse slack event"))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	switch eventsAPIEvent.Type {
	case slackevents.URLVerification:
		var r *slackevents.ChallengeResponse
		err := json.Unmarshal([]byte(body), &r)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text")
		w.Write([]byte(r.Challenge))
	case slackevents.CallbackEvent:
		innerEvent := eventsAPIEvent.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			sl.client.PostMessage(ev.Channel, slack.MsgOptionText("Yes, hello.", false))
		case *slackevents.ReactionAddedEvent:
			if ev.User == sl.myUserID {
				log.Tracef("Ignoring reaction %S from myself %s", ev.Reaction, ev.User)
				return
			}
			log.Tracef("Received reaction, channel: %s, reaction: %s, user: %s, item type: %s", ev.Item.Channel, ev.Reaction, ev.User, ev.Item.Type)
			if !(ev.Reaction == sl.EmojiName && ev.Item.Type == "message") {
				log.Tracef("Ignore reaction %s and type %s", ev.Reaction, ev.Item.Type)
				return
			}
			sl.EventDestination <- ev
		default:
			log.Info("Received unexpected innerevent: " + innerEvent.Type)
		}
	default:
		log.Info("Received unexpected slackevent: " + eventsAPIEvent.Type)
	}
}
