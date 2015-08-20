// An HTTP Client which sends json and binary requests, handling data marshalling and response processing.

package httpclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	log "github.com/cihub/seelog"
	errors "github.com/gdrte/httpclient/errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

const (
	contentTypeJSON        = "application/json"
	contentTypeOctetStream = "application/octet-stream"
	// The HTTP request methods.
	GET    = "GET"
	POST   = "POST"
	PUT    = "PUT"
	DELETE = "DELETE"
	HEAD   = "HEAD"
	COPY   = "COPY"
)

type Client struct {
	http.Client
	maxSendAttempts int
	agentName       string
}

type ErrorResponse struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
	Title   string `json:"title"`
}

func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("Failed: %d %s: %s", e.Code, e.Title, e.Message)
}

type ErrorWrapper struct {
	Error ErrorResponse `json:"error"`
}

type RequestData struct {
	ReqHeaders     http.Header
	Params         *url.Values
	ExpectedStatus []int
	ReqValue       interface{}
	RespValue      interface{}
	StatusCode     int
	ReqReader      io.Reader
	ReqLength      int
	RespReader     io.ReadCloser
	UnMarshalJson  bool
	Binary         bool
}

const (
	// The maximum number of times to try sending a request before we give up
	// (assuming any unsuccessful attempts can be sensibly tried again).
	MaxSendAttempts = 3
)

// New returns a new goose http *Client using the default net/http client.
func New(agentName string) *Client {
	return &Client{*http.DefaultClient, MaxSendAttempts, agentName}
}

func (c *Client) createHeaders(extraHeaders http.Header, contentType string) http.Header {
	headers := make(http.Header)
	if extraHeaders != nil {
		for header, values := range extraHeaders {
			for _, value := range values {
				headers.Add(header, value)
			}
		}
	}
	headers.Add("Content-Type", contentType)
	headers.Add("Accept", contentType)
	headers.Add("User-Agent", c.agentName)
	return headers
}

func (c *Client) PlainRequest(method, url string, reqData *RequestData) (resp []byte, err error) {
	err = nil
	var body []byte
	if reqData.Params != nil {
		url += "?" + reqData.Params.Encode()
	}
	if sbody, ok := reqData.ReqValue.(string); ok {
		body = []byte(sbody)
	} else if reqData.ReqValue != nil {
		body, err = json.Marshal(reqData.ReqValue)
		if err != nil {
			err = errors.Newf(err, "failed marshalling the request body")
			return
		}
	}
	headers := c.createHeaders(reqData.ReqHeaders, "text/plain")
	respBody, statusCode, err := c.sendRequest(method, url, bytes.NewReader(body), len(body), headers, reqData.ExpectedStatus)
	reqData.StatusCode = statusCode
	resp, err = ioutil.ReadAll(respBody)
	return
}

// JsonRequest JSON encodes and sends the object in reqData.ReqValue (if any) to the specified URL.
// Optional method arguments are passed using the RequestData object.
// Relevant RequestData fields:
// ReqHeaders: additional HTTP header values to add to the request.
// ExpectedStatus: the allowed HTTP response status values, else an error is returned.
// ReqValue: the data object to send.
// RespValue: the data object to decode the result into.
func (c *Client) JsonRequest(method, url string, reqData *RequestData) (err error) {
	err = nil
	var body []byte
	if reqData.Params != nil {
		url += "?" + reqData.Params.Encode()
	}
	if sbody, ok := reqData.ReqValue.(string); ok {
		body = []byte(sbody)
	} else if reqData.ReqValue != nil {
		body, err = json.Marshal(reqData.ReqValue)
		if err != nil {
			err = errors.Newf(err, "failed marshalling the request body")
			return
		}
	}
	headers := c.createHeaders(reqData.ReqHeaders, contentTypeJSON)
	respBody, statusCode, err := c.sendRequest(
		method, url, bytes.NewReader(body), len(body), headers, reqData.ExpectedStatus)
	reqData.StatusCode = statusCode
	log.Tracef("%s:%s", method, url)

	if err != nil {
		return
	}
	err = unmarshallResponse(respBody, reqData)
	return
}

func unmarshallResponse(respBody io.ReadCloser, reqData *RequestData) (err error) {
	defer respBody.Close()
	respData, err := ioutil.ReadAll(respBody)
	if err != nil {
		err = errors.Newf(err, "failed reading the response body")
		return
	}
	if len(respData) > 0 {
		if reqData.RespValue != nil {
			err = json.Unmarshal(respData, &reqData.RespValue)
			if err != nil {
				err = errors.Newf(err, "failed unmarshaling the response body: %s", respData)
			}
		}
	}
	return
}

// Sends the byte array in reqData.ReqValue (if any) to the specified URL.
// Optional method arguments are passed using the RequestData object.
// Relevant RequestData fields:
// ReqHeaders: additional HTTP header values to add to the request.
// ExpectedStatus: the allowed HTTP response status values, else an error is returned.
// ReqReader: an io.Reader providing the bytes to send.
// RespReader: assigned an io.ReadCloser instance used to read the returned data..
func (c *Client) BinaryRequest(method, url, token string, reqData *RequestData) (err error) {
	err = nil
	if reqData.Params != nil {
		url += "?" + reqData.Params.Encode()
	}
	headers := c.createHeaders(reqData.ReqHeaders, contentTypeOctetStream)
	respBody, statusCode, err := c.sendRequest(
		method, url, reqData.ReqReader, reqData.ReqLength, headers, reqData.ExpectedStatus)
	reqData.StatusCode = statusCode
	if err != nil {
		return
	}
	if reqData.RespReader != nil {
		reqData.RespReader = respBody
	}
	if reqData.UnMarshalJson {
		err = unmarshallResponse(respBody, reqData)
	}
	return
}

// Sends the specified request to URL and checks that the HTTP response status is as expected.
// reqReader: a reader returning the data to send.
// length: the number of bytes to send.
// headers: HTTP headers to include with the request.
// expectedStatus: a slice of allowed response status codes.
func (c *Client) sendRequest(method, URL string, reqReader io.Reader, length int, headers http.Header,
	expectedStatus []int) (rc io.ReadCloser, statusCode int, err error) {
	rawResp, err := c.sendRateLimitedRequest(method, URL, headers, reqReader)
	if err != nil {
		return
	}
	foundStatus := false
	if len(expectedStatus) == 0 {
		expectedStatus = []int{http.StatusOK}
	}

	for _, status := range expectedStatus {
		if rawResp.StatusCode == status {
			foundStatus = true
			break
		}
	}
	if !foundStatus && len(expectedStatus) > 0 {
		err = handleError(URL, rawResp)
		rawResp.Body.Close()
		return
	}
	return rawResp.Body, rawResp.StatusCode, err
}

func (c *Client) sendRateLimitedRequest(method, URL string, headers http.Header, reqReader io.Reader, /*reqData []byte,*/
) (resp *http.Response, err error) {
	for i := 0; i < c.maxSendAttempts; i++ {
		/*
		 * var reqReader io.Reader
		 * if reqData != nil {
		 *	reqReader = bytes.NewReader(reqData)
		 * }
		 */
		req, err := http.NewRequest(method, URL, reqReader)
		if err != nil {
			err = errors.Newf(err, "failed creating the request %s", URL)
			return nil, err
		}
		for header, values := range headers {
			for _, value := range values {
				req.Header.Add(header, value)
			}
		}

		resp, err = c.Do(req)
		log.Tracef("%s: %s", method, URL)
		if err != nil {
			return nil, errors.Newf(err, "failed executing the request %s", URL)
		}
		if resp.StatusCode != http.StatusRequestEntityTooLarge || resp.Header.Get("Retry-After") == "" {
			return resp, nil
		}
		resp.Body.Close()
		retryAfter, err := strconv.ParseFloat(resp.Header.Get("Retry-After"), 32)
		if err != nil {
			return nil, errors.Newf(err, "Invalid Retry-After header %s", URL)
		}
		if retryAfter == 0 {
			return nil, errors.Newf(err, "Resource limit exeeded at URL %s", URL)
		}
		log.Debugf("Too many requests, retrying in %dms.", int(retryAfter*1000))

		time.Sleep(time.Duration(retryAfter) * time.Second)
	}
	return nil, errors.Newf(err, "Maximum number of attempts (%d) reached sending request to %s", c.maxSendAttempts, URL)
}

type HttpError struct {
	StatusCode      int
	Data            map[string][]string
	url             string
	responseMessage string
}

func (e *HttpError) Error() string {
	return fmt.Sprintf("request (%s) returned unexpected status: %d; error info: %v",
		e.url,
		e.StatusCode,
		e.responseMessage,
	)
}

// The HTTP response status code was not one of those expected, so we construct an error.
// NotFound (404) codes have their own NotFound error type.
// We also make a guess at duplicate value errors.
func handleError(URL string, resp *http.Response) error {
	errBytes, _ := ioutil.ReadAll(resp.Body)
	errInfo := string(errBytes)
	// Check if we have a JSON representation of the failure, if so decode it.
	if resp.Header.Get("Content-Type") == contentTypeJSON {
		var wrappedErr ErrorWrapper
		if err := json.Unmarshal(errBytes, &wrappedErr); err == nil {
			errInfo = wrappedErr.Error.Error()
		}
	}
	httpError := &HttpError{
		resp.StatusCode, map[string][]string(resp.Header), URL, errInfo,
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return errors.NewNotFoundf(httpError, "", "Resource at %s not found", URL)
	case http.StatusForbidden, http.StatusUnauthorized:
		return errors.NewUnauthorisedf(httpError, "", "Unauthorised URL %s", URL)
	case http.StatusBadRequest:
		dupExp, _ := regexp.Compile(".*already exists.*")
		if dupExp.Match(errBytes) {
			return errors.NewDuplicateValuef(httpError, "", string(errBytes))
		}
	}
	return httpError
}
