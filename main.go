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
		JiraEndpoint           string `arg:"--jira-endpoint,env:JIRA_ENDPOINT" help:"URL to hit with Jira"`
		JiraUsername           string `arg:"--jira-username,env:JIRA_USERNAME" help:"Jira usernaem to use"`
		JiraPassword           string `arg:"--jira-password,env:JIRA_PASSWORD" help:"Jira password to use"`

		UserJiraPairs      string `arg:"-u,--user-jira-pairs,env:USER_JIRA_PAIRS" help:"Comma separated list of email/jira project pairs. For example: user@example.com=SYS,bob@example.com=PROJ`
		DefaultEmailDomain string `arg:"--default-email-domain,env:DEFAULT_EMAIL_DOMAIN" help:"Default domain if you do not provide an @ sybmol in a user/jira pair"`
		ActuallyCreate     string `arg:"--actually-create,env:ACTUALLY_CREATE" default:"false" help:"Set to true to actually create jira ticekts"`
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

	queue := make(chan *slackevents.ReactionAddedEvent, 1)

	// Create the listener
	sl := newSlackListener(args.SlackToken, queue)
	sl.VerificationToken = args.SlackVerificationToken
	sl.EmojiName = args.EmojiName

	// Create the jira connection
	tc := newJiraHandler(args.JiraEndpoint, args.JiraUsername, args.JiraPassword)
	if strings.ToLower(args.ActuallyCreate) == "true" {
		tc.ActuallyCreate = true
	}

	// Create the handler
	sh := newSlackHandler(args.SlackToken, queue, tc)
	sh.VerificationToken = args.SlackVerificationToken
	sh.CompletedReaction = args.EmojiName

	for _, ujp := range strings.Split(args.UserJiraPairs, ",") {
		ujpArr := strings.Split(ujp, "=")
		if len(ujpArr) != 2 {
			log.Error("Invalid user-jira-pair: " + ujp)
			continue
		}
		user := ujpArr[0]
		project := ujpArr[1]

		if !strings.Contains(user, "@") {
			user = user + "@" + strings.TrimLeft(args.DefaultEmailDomain, "@")
		}
		log.Tracef("Adding user %s for project %s", user, project)
		sh.userJiraPairs[user] = project
	}

	go sh.HandleEvents()

	// Start listenign
	http.HandleFunc("/slack", sl.handler)
	http.HandleFunc("/health", sl.healthHandler)
	http.HandleFunc("/", genericHandler)
	log.Infof("Going to listen on port :%d", args.Port)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", args.Port), nil))
}
