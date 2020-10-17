package main

import (
	"fmt"

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

	client        *slack.Client
	userCache     map[string]*slack.User
	userJiraPairs map[string]string
}

func newSlackHandler(token string, queue <-chan *slackevents.ReactionAddedEvent, tc TicketCreator) *SlackHandler {
	return &SlackHandler{
		client:        slack.New(token),
		EventQueue:    queue,
		userCache:     make(map[string]*slack.User),
		userJiraPairs: make(map[string]string),
		TicketCreator: tc,
	}
}

func (sh *SlackHandler) HandleEvents() {
	for {
		select {
		case ev := <-sh.EventQueue:
			err := sh.handleEvent(ev)
			if err != nil {
				log.Error(errors.Wrap(err, "Failed to handle event"))
			}
		}
	}
}

func (sh *SlackHandler) handleEvent(ev *slackevents.ReactionAddedEvent) error {
	var err error
	messageRef := slack.ItemRef{
		Channel:   ev.Item.Channel,
		Timestamp: ev.Item.Timestamp,
	}
	sh.client.RemoveReaction("x", messageRef)

	sh.client.AddReaction("hourglass_flowing_sand", messageRef)
	defer sh.client.RemoveReaction("hourglass_flowing_sand", messageRef)

	user, ok := sh.userCache[ev.User]
	if !ok {
		user, err = sh.client.GetUserInfo(ev.User)
		if err != nil {
			sh.client.AddReaction("x", messageRef)
			return errors.Wrap(err, "get user info")
		}
		sh.userCache[ev.User] = user
	}
	log.Debugf("Got actionable reaction %s from user %s", ev.Reaction, user.Profile.Email)
	jiraProject, ok := sh.userJiraPairs[user.Profile.Email]
	if !ok {
		sh.client.AddReaction("x", messageRef)
		return fmt.Errorf("User %s not configured in user_jira_pairs", user.Profile.Email)
	}

	log.Debugf("Need to create a ticket in %s", jiraProject)

	origMessage, err := sh.fetchMessage(ev.Item.Channel, ev.Item.Timestamp)
	if err != nil {
		sh.client.AddReaction("x", messageRef)
		return errors.Wrap(err, "Failed to get original message")
	}

	threadParent := slack.ItemRef{
		Channel:   origMessage.Channel,
		Timestamp: origMessage.Timestamp,
	}

	if origMessage.ThreadTimestamp != "" {
		// we are in a thread: https://api.slack.com/messaging/retrieving#threading
		log.Trace("This message is a thread parent or thread childe")
		if origMessage.ThreadTimestamp != origMessage.Timestamp {
			log.Trace("This message is not a thread parent")
			threadParent.Timestamp = origMessage.ThreadTimestamp
		}
	}

	messagePermalink, err := sh.client.GetPermalink(&slack.PermalinkParameters{
		Channel: origMessage.Channel,
		Ts:      origMessage.Timestamp,
	})
	if err != nil {
		sh.client.AddReaction("x", messageRef)
		return errors.Wrap(err, "Failed to get permalink")
	}

	ticketTitle := origMessage.Text
	if len(ticketTitle) > 100 {
		ticketTitle = ticketTitle[:100]
	}
	ticketContent := fmt.Sprintf("From slack: %s\n\n%s", messagePermalink, origMessage.Text)
	ticketID, err := sh.TicketCreator.CreateTicket(jiraProject, ticketTitle, ticketContent)
	var response string
	if err != nil {
		response = "There was an error creating the jira ticket."
		log.Error(errors.Wrap(err, "Creating jira ticket"))
	} else {
		response = fmt.Sprintf("I've created your jira ticket %s: https://jira.1e4h.net/browse/%s", ticketID, ticketID)
	}

	_, _, err = sh.client.PostMessage(
		threadParent.Channel,
		slack.MsgOptionTS(threadParent.Timestamp),
		slack.MsgOptionText(response, true),
	)
	if err != nil {
		sh.client.AddReaction("x", messageRef)
		return errors.Wrapf(err, "posting message to channel %s, ts %s", threadParent.Channel, threadParent.Timestamp)
	}
	return nil
}

func (sh *SlackHandler) fetchMessage(channel, timestamp string) (slack.Message, error) {
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
	return msg, nil
}
