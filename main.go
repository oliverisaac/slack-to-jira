package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/alexflint/go-arg"
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
	userCache         map[string]*slack.User
	userJiraPairs     map[string]string
}

func newSlackListener(slackToken string) *slackListener {
	sl := slackListener{
		Token:         slackToken,
		client:        slack.New(slackToken),
		userCache:     make(map[string]*slack.User),
		userJiraPairs: make(map[string]string),
	}
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
			log.Tracef("Received reaction, channel: %s, reaction: %s, user: %s, item type: %s", ev.Item.Channel, ev.Reaction, ev.User, ev.Item.Type)
			if !(ev.Reaction == sl.EmojiName && ev.Item.Type == "message") {
				log.Tracef("Ignore reaction %s and type %s", ev.Reaction, ev.Item.Type)
				return
			}

			messageRef := slack.ItemRef{
				Channel:   ev.Item.Channel,
				Timestamp: ev.Item.Timestamp,
			}

			sl.client.AddReaction("hourglass_flowing_sand", messageRef)
			defer sl.client.RemoveReaction("hourglass_flowing_sand", messageRef)

			user, ok := sl.userCache[ev.User]
			if !ok {
				user, err = sl.client.GetUserInfo(ev.User)
				if err != nil {
					sl.client.AddReaction("x", messageRef)
					log.Error(errors.Wrap(err, "get user info"))
					return
				}
				sl.userCache[ev.User] = user
			}
			log.Debugf("Got actionable reaction %s from user %s", ev.Reaction, user.Profile.Email)
			jiraProject, ok := sl.userJiraPairs[user.Profile.Email]
			if !ok {
				sl.client.AddReaction("x", messageRef)
				return
			}

			log.Debugf("Need to create a ticket in %s", jiraProject)
		default:
			log.Info("Received unexpected innerevent: " + innerEvent.Type)
		}
	default:
		log.Info("Received unexpected slackevent: " + eventsAPIEvent.Type)
	}
}

func genericHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello world"))
}

func main() {
	var args struct {
		LogLevel               string `arg:"--log-level,env:LOG_LEVEL" default:"info" help:"Set log level, one of: trace, debug, info, warn, error, fatal"`
		LogFormat              string `arg:"--log-format,env:LOG_FORMAT" default:"text" help:"Set log format, one of: json, text"`
		Port                   int    `arg:"-p,--port,env" default:"8080" help:"Port to listen on"`
		EmojiName              string `arg:"-e,--emoji,env:EMOJI" default:"create-jira-ticket" help:"Emoji name to create a ticket for"`
		SlackToken             string `arg:"-s,--slack-token,env:SLACK_TOKEN" help:"Slack auth token"`
		SlackVerificationToken string `arg:"-f,--slack-verification-token,env:SLACK_VERIFICATION_TOKEN" default:"" help:"Slack verification token"`
		JiraToken              string `arg:"-j,--jira-token,env:JIRA_TOKEN" help:"Jira auth token"`
		UserJiraPairs          string `arg:"-u,--user-jira-pairs,env:USER_JIRA_PAIRS" help:"Comma separated list of email/jira project pairs. For example: user@example.com=SYS,bob@example.com=PROJ`
	}
	arg.MustParse(&args)

	log.SetLevel(log.InfoLevel)
	if ll, err := log.ParseLevel(args.LogLevel); err == nil {
		log.SetLevel(ll)
	}

	switch args.LogFormat {
	case "json":
		log.SetFormatter(&log.JSONFormatter{})
	case "text":
		log.SetFormatter(&log.TextFormatter{})
	default:
		log.SetFormatter(&log.TextFormatter{})
		log.Error("Unknown log format given: " + args.LogFormat)
	}

	sl := newSlackListener(args.SlackToken)
	sl.VerificationToken = args.SlackVerificationToken
	sl.EmojiName = args.EmojiName

	for _, ujp := range strings.Split(args.UserJiraPairs, ",") {
		ujpArr := strings.Split(ujp, "=")
		if len(ujpArr) != 2 {
			log.Error("Invalid user-jira-pair: " + ujp)
			continue
		}
		sl.userJiraPairs[ujpArr[0]] = ujpArr[1]
	}

	http.HandleFunc("/slack", sl.handler)
	http.HandleFunc("/health", sl.healthHandler)
	http.HandleFunc("/", genericHandler)
	log.Infof("Going to listen on port :%d", args.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", args.Port), nil))
}
