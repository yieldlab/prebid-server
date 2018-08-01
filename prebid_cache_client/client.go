package prebid_cache_client

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"

	"github.com/tidwall/gjson"

	"github.com/golang/glog"
	"github.com/prebid/prebid-server/config"
	"golang.org/x/net/context/ctxhttp"
)

// Client stores values in Prebid Cache. For more info, see https://github.com/prebid/prebid-cache
type Client interface {
	// PutJson stores JSON values for the given openrtb.Bids in the cache. Null values will be
	//
	// The returned string slice will always have the same number of elements as the values argument. If a
	// value could not be saved, the element will be an empty string. Implementations are responsible for
	// logging any relevant errors to the app logs
	PutJson(ctx context.Context, values []json.RawMessage) []string
}

func NewClient(conf *config.Cache) Client {
	return &clientImpl{
		httpClient: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:    10,
				IdleConnTimeout: 65,
			},
		},
		putUrl: conf.GetBaseURL() + "/cache",
	}
}

type clientImpl struct {
	httpClient *http.Client
	putUrl     string
}

func (c *clientImpl) PutJson(ctx context.Context, values []json.RawMessage) (uuids []string) {
	if len(values) < 1 {
		return nil
	}

	uuidsToReturn := make([]string, len(values))

	postBody, err := encodeValues(values)
	if err != nil {
		glog.Errorf("Error creating JSON for prebid cache: %v", err)
		return uuidsToReturn
	}
	httpReq, err := http.NewRequest("POST", c.putUrl, bytes.NewReader(postBody))
	if err != nil {
		glog.Errorf("Error creating POST request to prebid cache: %v", err)
		return uuidsToReturn
	}
	httpReq.Header.Add("Content-Type", "application/json;charset=utf-8")
	httpReq.Header.Add("Accept", "application/json")

	anResp, err := ctxhttp.Do(ctx, c.httpClient, httpReq)
	if err != nil {
		glog.Errorf("Error sending the request to Prebid Cache: %v", err)
		return uuidsToReturn
	}
	defer anResp.Body.Close()

	responseBody, err := ioutil.ReadAll(anResp.Body)
	if anResp.StatusCode != 200 {
		glog.Errorf("Prebid Cache call to %s returned %d: %s", putURL, anResp.StatusCode, responseBody)
		return uuidsToReturn
	}

	if !gjson.ValidBytes(responseBody) {
		glog.Errorf("Prebid Cache response body was not valid JSON: %s", err, string(responseBody))
		return uuidsToReturn
	}

	responses := gjson.GetBytes(responseBody, "responses")
	if !responses.IsArray() {
		glog.Errorf("Prebid Cache responseBody.responses was not a JSON array: %s", err, string(responseBody))
		return uuidsToReturn
	}

	currentIndex := 0
	responses.ForEach(func(_ gjson.Result, response gjson.Result) bool {
		id := response.Get("uuid")
		if id.Type != gjson.String {
			glog.Errorf("Prebid Cache responseBody.responses had a malformed element. Skipping this. Response was: %s", string(responseBody))
			currentIndex++
			return true
		}
		uuidsToReturn[currentIndex] = id.String()
		currentIndex++
		return true
	})

	return uuidsToReturn
}

func encodeValues(values []json.RawMessage) ([]byte, error) {
	// This function assumes that m is non-nil and has at least one element.
	// clientImp.PutBids should respect this.
	var buf bytes.Buffer
	buf.WriteString(`{"puts":[`)
	for i := 0; i < len(values); i++ {
		if err := encodeValueToBuffer(values[i], i != 0, &buf); err != nil {
			return nil, err
		}
	}
	buf.WriteString("]}")
	return buf.Bytes(), nil
}

func encodeValueToBuffer(value json.RawMessage, leadingComma bool, buffer *bytes.Buffer) error {
	if leadingComma {
		buffer.WriteByte(',')
	}

	encodedBytes, err := json.Marshal(value)
	if err != nil {
		return err
	} else {
		buffer.WriteString(`{"type":"json","value":`)
		buffer.Write(encodedBytes)
		buffer.WriteByte('}')
	}
	return nil
}
