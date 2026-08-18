package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

type fakeVectorRetriever struct {
	calls int
	docs  []*schema.Document
	err   error
}

func (f *fakeVectorRetriever) Retrieve(context.Context, string, ...retriever.Option) ([]*schema.Document, error) {
	f.calls++
	return f.docs, f.err
}

type fakeMilvusCountQuerier struct {
	result client.ResultSet
	err    error
	calls  int
	expr   string
}

func (f *fakeMilvusCountQuerier) Query(_ context.Context, _ string, _ []string, expr string, _ []string, _ ...client.SearchQueryOptionFunc) (client.ResultSet, error) {
	f.calls++
	f.expr = expr
	return f.result, f.err
}

func countResult(value int64) client.ResultSet {
	return client.ResultSet{entity.NewColumnInt64("count(*)", []int64{value})}
}

func TestRetrieveByUserSkipsVectorSearchWhenUserHasNoDocuments(t *testing.T) {
	vector := &fakeVectorRetriever{err: errors.New("vector search must not run")}
	count := &fakeMilvusCountQuerier{result: countResult(0)}
	store := &MilvusStore{retriever: vector, countQuerier: count}

	docs, err := store.RetrieveByUser(context.Background(), "alice", "Go scheduler")
	if err != nil {
		t.Fatalf("RetrieveByUser() error = %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("RetrieveByUser() returned %d documents, want 0", len(docs))
	}
	if vector.calls != 0 {
		t.Fatalf("vector retriever calls = %d, want 0", vector.calls)
	}
	if count.calls != 1 || count.expr != `user_id == "alice"` {
		t.Fatalf("count query calls=%d expr=%q", count.calls, count.expr)
	}
}

func TestRetrieveByUserSearchesWhenUserHasDocuments(t *testing.T) {
	want := []*schema.Document{{ID: "question-1", Content: "question"}}
	vector := &fakeVectorRetriever{docs: want}
	count := &fakeMilvusCountQuerier{result: countResult(1)}
	store := &MilvusStore{retriever: vector, countQuerier: count}

	docs, err := store.RetrieveByUser(context.Background(), "alice", "Go scheduler")
	if err != nil {
		t.Fatalf("RetrieveByUser() error = %v", err)
	}
	if len(docs) != 1 || docs[0].ID != want[0].ID {
		t.Fatalf("RetrieveByUser() = %#v, want %#v", docs, want)
	}
	if vector.calls != 1 {
		t.Fatalf("vector retriever calls = %d, want 1", vector.calls)
	}
}

func TestRetrieveByUserFallsBackToSearchWhenCountFails(t *testing.T) {
	wantErr := errors.New("search failed")
	vector := &fakeVectorRetriever{err: wantErr}
	count := &fakeMilvusCountQuerier{err: errors.New("count unavailable")}
	store := &MilvusStore{retriever: vector, countQuerier: count}

	_, err := store.RetrieveByUser(context.Background(), "alice", "Go scheduler")
	if !errors.Is(err, wantErr) {
		t.Fatalf("RetrieveByUser() error = %v, want %v", err, wantErr)
	}
	if vector.calls != 1 {
		t.Fatalf("vector retriever calls = %d, want 1", vector.calls)
	}
}
