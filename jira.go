package main

import (
	"io/ioutil"
	"strings"

	"github.com/andygrunwald/go-jira"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

type JiraHandler struct {
	client *jira.Client
}

func newJiraHandler(endpoint, username, password string) *JiraHandler {
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: password,
	}

	client, err := jira.NewClient(tp.Client(), endpoint)
	if err != nil {
		log.Fatal(errors.Wrap(err, "Failed to create jira client"))
	}

	return &JiraHandler{
		client: client,
	}
}

func (jh *JiraHandler) CreateTicket(project, title, description string) (string, error) {
	issue := &jira.Issue{
		Fields: &jira.IssueFields{
			Project: jira.Project{
				Key: project,
			},
			Summary:     strings.ReplaceAll(title, "\n", " "),
			Description: description,
			Type: jira.IssueType{
				Name: "Task",
			},
		},
	}
	issueResp, jiraResponse, err := jh.client.Issue.Create(issue)
	defer jiraResponse.Body.Close()

	if err != nil {
		respBody, _ := ioutil.ReadAll(jiraResponse.Body)
		log.Error("Jira response body: " + string(respBody))
		return "", errors.Wrap(err, "Error creating issue")
	}
	return issueResp.Key, nil
}
