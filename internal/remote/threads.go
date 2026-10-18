// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Comment is one review comment as the sync layer sees it.
type Comment struct {
	ID              int64
	Body            string
	ViewerDidAuthor bool
	ViewerCanUpdate bool
}

// Thread is a review thread snapshot (GraphQL `reviewThreads`). Root is the
// first comment (it carries the identity marker); Recent is the tail of the
// thread, oldest first, so its last element is the thread's latest comment.
type Thread struct {
	NodeID       string
	Path         string
	Resolved     bool
	Outdated     bool
	CanResolve   bool
	CanUnresolve bool
	CanReply     bool
	Root         Comment
	Recent       []Comment
}

// LastID is the database id of the thread's most recent comment.
func (t Thread) LastID() int64 {
	if n := len(t.Recent); n > 0 {
		return t.Recent[n-1].ID
	}
	return t.Root.ID
}

const reviewThreadsQuery = `query($owner:String!,$name:String!,$number:Int!,$endCursor:String){
  repository(owner:$owner,name:$name){
    pullRequest(number:$number){
      reviewThreads(first:100,after:$endCursor){
        pageInfo{hasNextPage endCursor}
        nodes{
          id path isResolved isOutdated viewerCanResolve viewerCanUnresolve viewerCanReply
          comments(first:1){nodes{databaseId body viewerDidAuthor viewerCanUpdate}}
          recent: comments(last:10){nodes{databaseId body viewerDidAuthor}}
        }
      }
    }
  }
}`

type gqlComment struct {
	DatabaseID      int64  `json:"databaseId"`
	Body            string `json:"body"`
	ViewerDidAuthor bool   `json:"viewerDidAuthor"`
	ViewerCanUpdate bool   `json:"viewerCanUpdate"`
}

type gqlThread struct {
	ID               string `json:"id"`
	Path             string `json:"path"`
	IsResolved       bool   `json:"isResolved"`
	IsOutdated       bool   `json:"isOutdated"`
	ViewerCanResolve bool   `json:"viewerCanResolve"`
	ViewerCanUnres   bool   `json:"viewerCanUnresolve"`
	ViewerCanReply   bool   `json:"viewerCanReply"`
	Comments         struct {
		Nodes []gqlComment `json:"nodes"`
	} `json:"comments"`
	Recent struct {
		Nodes []gqlComment `json:"nodes"`
	} `json:"recent"`
}

type gqlPage struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Data struct {
		Repository struct {
			PullRequest *struct {
				ReviewThreads struct {
					Nodes []gqlThread `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
		} `json:"repository"`
	} `json:"data"`
}

// FetchThreads reads every review thread of a PR through GraphQL. `gh api
// graphql --paginate` emits one JSON document per page, back to back, so the
// output is decoded as a stream. Empty output is an empty snapshot.
func FetchThreads(ctx context.Context, r Runner, slug string, number int) ([]Thread, error) {
	owner, name, ok := strings.Cut(slug, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("invalid repo slug %q", slug)
	}
	out, err := r.Run(ctx,
		"api", "graphql", "--paginate",
		"-f", "query="+reviewThreadsQuery,
		"-f", "owner="+owner,
		"-f", "name="+name,
		"-F", "number="+strconv.Itoa(number),
	)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var threads []Thread
	for {
		var page gqlPage
		if err := dec.Decode(&page); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode review threads: %w", err)
		}
		if len(page.Errors) > 0 {
			return nil, fmt.Errorf("review threads: %s", page.Errors[0].Message)
		}
		pr := page.Data.Repository.PullRequest
		if pr == nil {
			return nil, errors.New("review threads: pull request not found")
		}
		for _, n := range pr.ReviewThreads.Nodes {
			if len(n.Comments.Nodes) == 0 {
				continue
			}
			root := n.Comments.Nodes[0]
			t := Thread{
				NodeID:       n.ID,
				Path:         n.Path,
				Resolved:     n.IsResolved,
				Outdated:     n.IsOutdated,
				CanResolve:   n.ViewerCanResolve,
				CanUnresolve: n.ViewerCanUnres,
				CanReply:     n.ViewerCanReply,
				Root: Comment{
					ID:              root.DatabaseID,
					Body:            root.Body,
					ViewerDidAuthor: root.ViewerDidAuthor,
					ViewerCanUpdate: root.ViewerCanUpdate,
				},
			}
			for _, c := range n.Recent.Nodes {
				t.Recent = append(t.Recent, Comment{ID: c.DatabaseID, Body: c.Body, ViewerDidAuthor: c.ViewerDidAuthor})
			}
			threads = append(threads, t)
		}
	}
	return threads, nil
}

const (
	resolveThreadMutation   = `mutation($id:ID!){resolveReviewThread(input:{threadId:$id}){thread{id isResolved}}}`
	unresolveThreadMutation = `mutation($id:ID!){unresolveReviewThread(input:{threadId:$id}){thread{id isResolved}}}`
)

func threadMutation(ctx context.Context, r Runner, query, nodeID string) error {
	out, err := r.Run(ctx, "api", "graphql", "-f", "query="+query, "-f", "id="+nodeID)
	if err != nil {
		return err
	}
	var resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if len(bytes.TrimSpace(out)) > 0 && json.Unmarshal(out, &resp) == nil && len(resp.Errors) > 0 {
		return errors.New(resp.Errors[0].Message)
	}
	return nil
}

// ResolveThread marks a review thread resolved.
func ResolveThread(ctx context.Context, r Runner, nodeID string) error {
	return threadMutation(ctx, r, resolveThreadMutation, nodeID)
}

// UnresolveThread reopens a resolved review thread.
func UnresolveThread(ctx context.Context, r Runner, nodeID string) error {
	return threadMutation(ctx, r, unresolveThreadMutation, nodeID)
}

// UpdateComment replaces the body of one review comment (REST PATCH).
func UpdateComment(ctx context.Context, r Runner, slug string, commentID int64, body string) error {
	_, err := r.Run(ctx,
		"api", "--method", "PATCH",
		"-H", "Accept: application/vnd.github+json",
		"-H", "X-GitHub-Api-Version: 2022-11-28",
		fmt.Sprintf("/repos/%s/pulls/comments/%d", slug, commentID),
		"-f", "body="+body,
	)
	return err
}

// ReplyToComment posts a reply into the thread rooted at commentID.
func ReplyToComment(ctx context.Context, r Runner, slug string, number int, commentID int64, body string) error {
	_, err := r.Run(ctx,
		"api", "--method", "POST",
		"-H", "Accept: application/vnd.github+json",
		"-H", "X-GitHub-Api-Version: 2022-11-28",
		fmt.Sprintf("/repos/%s/pulls/%d/comments/%d/replies", slug, number, commentID),
		"-f", "body="+body,
	)
	return err
}
