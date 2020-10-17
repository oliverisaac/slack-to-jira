package main

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/alexflint/go-arg"
	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack/slackevents"
)

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

	queue := make(chan *slackevents.ReactionAddedEvent)

	// Create the listener
	sl := newSlackListener(args.SlackToken, queue)
	sl.VerificationToken = args.SlackVerificationToken
	sl.EmojiName = args.EmojiName

	// Create the handler
	sh := newSlackHandler(args.SlackToken, queue)
	sh.VerificationToken = args.SlackVerificationToken

	for _, ujp := range strings.Split(args.UserJiraPairs, ",") {
		ujpArr := strings.Split(ujp, "=")
		if len(ujpArr) != 2 {
			log.Error("Invalid user-jira-pair: " + ujp)
			continue
		}
		sh.userJiraPairs[ujpArr[0]] = ujpArr[1]
	}

	go sh.HandleEvents()

	// Start listenign
	http.HandleFunc("/slack", sl.handler)
	http.HandleFunc("/health", sl.healthHandler)
	http.HandleFunc("/", genericHandler)
	log.Infof("Going to listen on port :%d", args.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", args.Port), nil))
}
