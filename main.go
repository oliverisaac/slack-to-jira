package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/alexflint/go-arg"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type slackListener struct {
	Token  string
	client *slack.Client
}

func newSlackListener(slackToken string) *slackListener {
	sl := slackListener{
		Token:  slackToken,
		client: slack.New(slackToken),
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
	eventsAPIEvent, err := slackevents.ParseEvent(json.RawMessage(body), slackevents.OptionVerifyToken(&slackevents.TokenComparator{VerificationToken: "VERIFICATION_TOKEN"}))

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
		}
	}
}

func genericHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("Hello world"))
}

func main() {
	var args struct {
		LogLevel   string `arg:"--log-level,env:LOG_LEVEL" default:"info" help:"Set log level, one of: trace, debug, info, warn, error, fatal"`
		LogFormat  string `arg:"--log-format,env:LOG_FORMAT" default:"text" help:"Set log format, one of: json, text"`
		Port       int    `arg:"-p,--port,env" default:"8080" help:"Port to listen on"`
		SlackToken string `arg:"-s,--slack-token,env:SLACK_TOKEN" help:"Slack auth token"`
		JiraToken  string `arg:"-j,--jira-token,env:JIRA_TOKEN" help:"Jira auth token"`
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

	http.HandleFunc("/slack", sl.handler)
	http.HandleFunc("/health", sl.healthHandler)
	http.HandleFunc("/", genericHandler)
	log.Infof("Going to listen on port :%d", args.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", args.Port), nil))
}
