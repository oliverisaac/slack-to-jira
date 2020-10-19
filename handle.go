package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type TicketCreator interface {
	CreateTicket(project, title, content string) (string, error)
}

type SlackHandler struct {
	Token             string
	VerificationToken string
	EventQueue        <-chan *slackevents.ReactionAddedEvent
	TicketCreator     TicketCreator
	CompletedReaction string

	myUserID      string
	client        *slack.Client
	userCache     map[string]*slack.User
	userJiraPairs map[string]string
	messageCache  map[string]slack.Message
}

func newSlackHandler(token string, queue <-chan *slackevents.ReactionAddedEvent, tc TicketCreator) *SlackHandler {
	sh := &SlackHandler{
		client:        slack.New(token),
		EventQueue:    queue,
		userCache:     make(map[string]*slack.User),
		userJiraPairs: make(map[string]string),
		messageCache:  make(map[string]slack.Message),
		TicketCreator: tc,
	}
	myProfile, err := sh.client.AuthTest()
	if err != nil {
		log.Fatal(errors.Wrap(err, "Running initial auth test"))
	}
	sh.myUserID = myProfile.UserID
	return sh
}

func (sh *SlackHandler) HandleEvents() {
	for {
		select {
		case ev := <-sh.EventQueue:
			messageRef := slack.ItemRef{
				Channel:   ev.Item.Channel,
				Timestamp: ev.Item.Timestamp,
			}
			sh.clearCache(ev.Item.Channel, ev.Item.Timestamp)
			err := sh.client.AddReaction("hourglass_flowing_sand", messageRef)
			if err != nil {
				log.Error(errors.Wrap(err, "Adding wait emoji"))
			}

			err = sh.client.RemoveReaction("x", messageRef)
			if err != nil {
				log.Error(errors.Wrap(err, "Removing error emoji"))
			}

			response, reaction, handleErr := sh.handleEvent(ev)
			if err != nil {
				log.Error(errors.Wrap(err, "Failed to handle event"))
				// If there's an error and a response we'll just send those
				if response == "" {
					err := sh.client.AddReaction("x", messageRef)
					if err != nil {
						log.Error(errors.Wrap(err, "Adding reaction 'x' on error"))
					}
				} else {
					err := sh.EphemeralCommentOnThread(ev.Item.Channel, ev.Item.Timestamp, ev.User, response)
					if err != nil {
						log.Error(errors.Wrap(err, "Ephemeral comment on thread"))
					}
				}
			}
			if reaction != "" {
				err := sh.client.AddReaction(reaction, messageRef)
				if err != nil {
					log.Error(errors.Wrap(err, "Adding reaction "+reaction))
				}
			}
			if response != "" && handleErr == nil {
				err := sh.CommentOnThread(ev.Item.Channel, ev.Item.Timestamp, response)
				if err != nil {
					log.Error(errors.Wrap(err, "Commenting on thread"))
				}
			}
			err = sh.client.RemoveReaction("hourglass_flowing_sand", messageRef)
			if err != nil {
				log.Error(errors.Wrap(err, "Removing wait emoji"))
			}
		}
	}
}

func (sh *SlackHandler) EphemeralCommentOnThread(channel, timestamp, user, comment string) error {
	origMessage, err := sh.fetchMessage(channel, timestamp)
	if err != nil {
		return errors.Wrap(err, "Fetch message")
	}
	targetTimestamp := timestamp
	if origMessage.ThreadTimestamp != "" {
		targetTimestamp = origMessage.ThreadTimestamp
	}
	_, err = sh.client.PostEphemeral(
		channel,
		user,
		slack.MsgOptionTS(targetTimestamp),
		slack.MsgOptionText(comment, true),
	)
	if err != nil {
		return errors.Wrap(err, "Ephemeral comment on thread")
	}
	return nil
}

func (sh *SlackHandler) CommentOnThread(channel, timestamp, comment string) error {
	origMessage, err := sh.fetchMessage(channel, timestamp)
	if err != nil {
		return errors.Wrap(err, "Fetch message")
	}
	targetTimestamp := timestamp
	if origMessage.ThreadTimestamp != "" {
		targetTimestamp = origMessage.ThreadTimestamp
	}
	_, _, err = sh.client.PostMessage(
		channel,
		slack.MsgOptionTS(targetTimestamp),
		slack.MsgOptionText(comment, true),
	)
	if err != nil {
		return errors.Wrap(err, "Comment on thread")
	}
	return nil
}

// handleEvent returns back a string and an error
// If the string is not empty, we post it to the original thread
func (sh *SlackHandler) handleEvent(ev *slackevents.ReactionAddedEvent) (message string, reaction string, err error) {
	user, ok := sh.userCache[ev.User]
	if !ok {
		user, err = sh.client.GetUserInfo(ev.User)
		if err != nil {
			return "", "", errors.Wrap(err, "get user info")
		}
		sh.userCache[ev.User] = user
	}
	log.Debugf("Got actionable reaction %s from user %s", ev.Reaction, user.Profile.Email)

	if user.Profile.Email == "" {
		return "Unable to get user info", "", fmt.Errorf("Unable to get user profile email from %s", ev.User)
	}

	jiraProject, ok := sh.userJiraPairs[user.Profile.Email]
	if !ok {
		message := fmt.Sprintf("Email %s is not configured in USER_JIRA_PAIRS", user.Profile.Email)
		return message, "", errors.New(message)
	}

	log.Debugf("Need to create a ticket in %s", jiraProject)
	origMessage, err := sh.fetchMessage(ev.Item.Channel, ev.Item.Timestamp)
	if err != nil {
		return "There was an error fetching the original message", "", errors.Wrap(err, "Failed to get original message")
	}

	for _, reaction := range origMessage.Reactions {
		log.Tracef("Looking at reaction %s", reaction.Name)
		if reaction.Name == sh.CompletedReaction {
			for _, u := range reaction.Users {
				log.Tracef("User %s reacted with %s", u, reaction.Name)
				if u == sh.myUserID {
					return "Jira ticket already created for this message", "", errors.New("Jira ticke already created")
				}
			}
		}
	}

	messagePermalink, err := sh.client.GetPermalink(&slack.PermalinkParameters{
		Channel: origMessage.Channel,
		Ts:      origMessage.Timestamp,
	})
	if err != nil {
		return "There was an error fetching the permalink", "", errors.Wrap(err, "Failed to get permalink")
	}

	ticketTitle := origMessage.Text
	ticketTitle = strings.Split(ticketTitle, "\n")[0]
	if len(ticketTitle) > 100 {
		ticketTitle = ticketTitle[:100]
	}
	ticketContent := fmt.Sprintf("From slack: %s\n\n%s", messagePermalink, origMessage.Text)
	ticketID, err := sh.TicketCreator.CreateTicket(jiraProject, ticketTitle, ticketContent)

	if err != nil {
		response := "There was an error creating the jira ticket."
		return response, "", errors.Wrap(err, "Creating jira ticket")
	}

	response := fmt.Sprintf("I've created your jira ticket %s: https://jira.1e4h.net/browse/%s", ticketID, ticketID)
	return response, sh.CompletedReaction, nil
}

func (sh *SlackHandler) fetchMessage(channel, timestamp string) (slack.Message, error) {
	cacheKey := fmt.Sprintf("%s:%s", channel, timestamp)
	if msg, ok := sh.messageCache[cacheKey]; ok {
		return msg, nil
	}

	messageArr, err := sh.client.GetConversationHistory(&slack.GetConversationHistoryParameters{
		ChannelID: channel,
		Latest:    timestamp,
		Limit:     1,
		Inclusive: true,
	})
	if err != nil {
		return slack.Message{}, errors.Wrap(err, "Failed to get message")
	}
	if len(messageArr.Messages) == 0 {
		return slack.Message{}, errors.New("Message response is 0")
	}
	msg := messageArr.Messages[0]
	msg.Channel = channel

	log.Trace("Settig cache key " + cacheKey)
	sh.messageCache[cacheKey] = msg
	// We clear the cache after 3 seconds
	go func() {
		time.Sleep(time.Second * 3)
		sh.clearCache(channel, timestamp)
	}()
	return msg, nil
}

func (sh *SlackHandler) clearCache(channel, timestamp string) {
	cacheKey := fmt.Sprintf("%s:%s", channel, timestamp)
	if _, ok := sh.messageCache[cacheKey]; ok {
		log.Trace("Clearing cache key " + cacheKey)
		delete(sh.messageCache, cacheKey)
	}
}
