// Copyright 2022 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	apiv1 "github.com/google/cloud-android-orchestration/api/v1"
)

const pageNotFoundErrMsg = "404 page not found\n"

const testUsername = "johndoe"

type testUserInfo struct{}

func (i *testUserInfo) Username() string { return testUsername }

type testAccountManager struct{}

func (m *testAccountManager) Authenticate(fn AuthHTTPHandler) HTTPHandler {
	return func(w http.ResponseWriter, r *http.Request) error {
		return fn(w, r, &testUserInfo{})
	}
}

func (m *testAccountManager) OnOAuthExchange(w http.ResponseWriter, r *http.Request, tk IDTokenClaims) (UserInfo, error) {
	return &testUserInfo{}, nil
}

type testInstanceManager struct {
	hostClientFactory func(zone, host string) HostClient
}

func (m *testInstanceManager) GetHostURL(zone string, host string) (*url.URL, error) {
	return url.Parse("http://127.0.0.1:8080")
}

func (m *testInstanceManager) CreateHost(_ string, _ *apiv1.CreateHostRequest, _ UserInfo) (*apiv1.Operation, error) {
	return &apiv1.Operation{}, nil
}

func (m *testInstanceManager) ListHosts(zone string, user UserInfo, req *ListHostsRequest) (*apiv1.ListHostsResponse, error) {
	return &apiv1.ListHostsResponse{}, nil
}

func (m *testInstanceManager) DeleteHost(zone string, user UserInfo, name string) (*apiv1.Operation, error) {
	return &apiv1.Operation{}, nil
}

func (m *testInstanceManager) WaitOperation(_ string, _ UserInfo, _ string) (any, error) {
	return struct{}{}, nil
}

func (m *testInstanceManager) GetHostClient(zone string, host string) (HostClient, error) {
	return m.hostClientFactory(zone, host), nil
}

type testHostClient struct {
	url *url.URL
}

func (hc *testHostClient) Get(path, query string, res *HostResponse) (int, error) {
	return 200, nil
}

func (hc *testHostClient) Post(path, query string, bodyJSON any, res *HostResponse) (int, error) {
	return 200, nil
}

func (hc *testHostClient) GetReverseProxy() *httputil.ReverseProxy {
	return httputil.NewSingleHostReverseProxy(hc.url)
}

func TestCreateHostSucceeds(t *testing.T) {
	controller := NewController(
		make([]string, 0), OperationsConfig{}, &testInstanceManager{}, nil, &testAccountManager{}, nil, nil, nil)
	ts := httptest.NewServer(controller.Handler())
	defer ts.Close()

	res, _ := http.Post(
		ts.URL+"/v1/zones/us-central1-a/hosts", "application/json", strings.NewReader("{}"))

	expected := http.StatusOK
	if res.StatusCode != expected {
		t.Errorf("unexpected status code <<%d>>, want: %d", res.StatusCode, expected)
	}
}

func TestCreateHostOperationIsDisabled(t *testing.T) {
	controller := NewController(
		make([]string, 0),
		OperationsConfig{CreateHostDisabled: true},
		&testInstanceManager{}, nil, &testAccountManager{}, nil, nil, nil)
	ts := httptest.NewServer(controller.Handler())
	defer ts.Close()

	res, _ := http.Post(
		ts.URL+"/v1/zones/us-central1-a/hosts", "application/json", strings.NewReader("{}"))

	expected := http.StatusMethodNotAllowed
	if res.StatusCode != expected {
		t.Errorf("unexpected status code <<%d>>, want: %d", res.StatusCode, expected)
	}
}

func TestWaitOperatioSucceeds(t *testing.T) {
	controller := NewController(
		make([]string, 0), OperationsConfig{}, &testInstanceManager{}, nil, &testAccountManager{}, nil, nil, nil)
	ts := httptest.NewServer(controller.Handler())
	defer ts.Close()

	res, _ := http.Post(
		ts.URL+"/v1/zones/us-central1-a/operations/foo/:wait", "application/json", strings.NewReader("{}"))

	expected := http.StatusOK
	if res.StatusCode != expected {
		t.Errorf("unexpected status code <<%d>>, want: %d", res.StatusCode, expected)
	}
}

func TestBuildListHostsRequest(t *testing.T) {

	t.Run("default", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "http://abc.com/", nil)

		listReq, _ := BuildListHostsRequest(r)

		if listReq.MaxResults != 0 {
			t.Errorf("expected <<%d>>, got %d", 0, listReq.MaxResults)
		}
		if listReq.PageToken != "" {
			t.Errorf("expected empty string, got %q", listReq.PageToken)
		}
	})

	t.Run("non integer maxResults", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "http://abc.com/query?maxResults=foo", nil)

		listReq, err := BuildListHostsRequest(r)

		assertIsAppError(t, err)
		if listReq != nil {
			t.Errorf("expected nil, got %+v", listReq)
		}
	})

	t.Run("negative integer maxResults", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "http://abc.com/query?maxResults=-1", nil)

		listReq, err := BuildListHostsRequest(r)

		assertIsAppError(t, err)
		if listReq != nil {
			t.Errorf("expected nil, got %+v", listReq)
		}
	})

	t.Run("full", func(t *testing.T) {
		r, _ := http.NewRequest("GET", "http://abc.com/query?pageToken=foo&maxResults=1", nil)

		listReq, _ := BuildListHostsRequest(r)

		expected := ListHostsRequest{
			MaxResults: 1,
			PageToken:  "foo",
		}
		if *listReq != expected {
			t.Errorf("expected <<%+v>>, got %+v", expected, listReq)
		}
	})
}

func TestDeleteHostIsHandled(t *testing.T) {
	rr := httptest.NewRecorder()
	req, err := http.NewRequest("DELETE", "/v1/zones/foo/hosts/bar", nil)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewController(
		make([]string, 0), OperationsConfig{}, &testInstanceManager{}, nil, &testAccountManager{}, nil, nil, nil)

	makeRequest(rr, req, controller)

	if rr.Code == http.StatusNotFound && rr.Body.String() == pageNotFoundErrMsg {
		t.Errorf("request was not handled. This failure implies an API breaking change.")
	}
}

func TestHostForwarderRequest(t *testing.T) {
	const headerContentType = "Content-Type"
	respContentType := "app/ct"
	respContent := "lorem ipsum"
	respStatusCode := http.StatusNotFound
	zone := "foo"
	host := "bar"
	resource := "devices"
	reqURL := fmt.Sprintf("http://test.com/v1/zones/%s/hosts/%s/%s?baz=1", zone, host, resource)
	postRequestBody := "duis feugiat"
	expectedReceivedURL := "/devices?baz=1"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.String() != expectedReceivedURL {
			t.Fatalf("expected url <<%q>>, got: %q", expectedReceivedURL, r.URL.String())
		}
		expectedBody := ""
		if r.Method == "POST" {
			expectedBody = postRequestBody
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != expectedBody {
			t.Fatalf("expected body <<%q>>, got: %q", expectedBody, string(b))
		}
		w.Header().Set("Content-Type", respContentType)
		w.WriteHeader(respStatusCode)
		w.Write([]byte(respContent))
	}))
	hostURL, _ := url.Parse(ts.URL)
	controller := NewController(
		make([]string, 0), OperationsConfig{}, &testInstanceManager{
			hostClientFactory: func(_, _ string) HostClient {
				return &testHostClient{hostURL}
			},
		}, nil, &testAccountManager{}, nil, nil, nil)

	tests := []struct {
		method  string
		reqBody string
	}{
		{method: "GET", reqBody: ""},
		{method: "POST", reqBody: postRequestBody},
	}

	for _, tt := range tests {

		t.Run(fmt.Sprintf("request - %s", tt.method), func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(tt.method, reqURL, bytes.NewBuffer([]byte(tt.reqBody)))

			makeRequest(w, req, controller)

			if w.Header()[headerContentType][0] != respContentType {
				t.Errorf("expected <<%q>>, got: %q", respContentType, w.Header()[headerContentType])
			}
			if w.Result().StatusCode != respStatusCode {
				t.Errorf("expected <<%+v>>, got: %+v", respStatusCode, w.Result().StatusCode)
			}
			b, _ := io.ReadAll(w.Result().Body)
			if string(b) != respContent {
				t.Errorf("expected <<%q>>, got: %q", respContent, string(b))
			}
		})
	}
}

func TestHostForwarderInvalidRequests(t *testing.T) {
	zone := "foo"
	host := "bar"
	cases := []string{
		"http://test.com/v1/zones",
		fmt.Sprintf("http://test.com/v1/zones/%s/hosts", zone),
	}
	for _, c := range cases {
		u, err := url.Parse(c)
		if err != nil {
			t.Errorf("Failed to parse test url: %v", err)
		}
		_, err = HostOrchestratorPath(u.Path, host)
		if err == nil {
			t.Errorf("Expected OrchestratorPath to fail")
		}
	}
}

func TestHostForwarderHostAsHostResource(t *testing.T) {
	host := "bar"
	reqURL := "http://test.com/v1/zones/foo/hosts/bar/hosts/bar"
	u, err := url.Parse(reqURL)
	if err != nil {
		t.Error(err)
	}
	expected := "/hosts/bar"
	path, err := HostOrchestratorPath(u.Path, host)
	if err != nil {
		t.Error(err)
	}
	if path != expected {
		t.Errorf("expected <<%q>>, got: %q", expected, path)
	}
}

func assertIsAppError(t *testing.T, err error) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Errorf("error type <<\"%T\">> not found in error chain", appErr)
	}
}

func makeRequest(w http.ResponseWriter, r *http.Request, controller *Controller) {
	router := controller.Handler()
	router.ServeHTTP(w, r)
}
