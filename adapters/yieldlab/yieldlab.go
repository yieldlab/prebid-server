package yieldlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/mxmCherry/openrtb"
	"golang.org/x/text/currency"

	"github.com/prebid/prebid-server/adapters"
	"github.com/prebid/prebid-server/errortypes"
	"github.com/prebid/prebid-server/openrtb_ext"
)

// YieldlabAdapter connects the Yieldlab API to prebid server
type YieldlabAdapter struct {
	endpoint    string
	cacheBuster cacheBuster
	getWeek     weekGenerator
}

// NewYieldlabBidder returns a new YieldlabBidder instance
func NewYieldlabBidder(endpoint string) *YieldlabAdapter {
	return &YieldlabAdapter{
		endpoint:    endpoint,
		cacheBuster: defaultCacheBuster,
		getWeek:     defaultWeekGenerator,
	}
}

// MakeRequests prepares bid requests to the Yieldlab API
func (a *YieldlabAdapter) MakeRequests(request *openrtb.BidRequest, reqInfo *adapters.ExtraRequestInfo) ([]*adapters.RequestData, []error) {
	var errs []error
	var adapterRequests []*adapters.RequestData

	adapterReq, errors := a.makeRequest(request)
	if adapterReq != nil {
		adapterRequests = append(adapterRequests, adapterReq)
	}
	errs = append(errs, errors...)

	return adapterRequests, errors
}

// Builds endpoint url based on adapter-specific pub settings from imp.ext
func (a *YieldlabAdapter) makeEndpointURL(req *openrtb.BidRequest, params *openrtb_ext.ExtImpYieldlab) (string, error) {
	uri, err := url.Parse(a.endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse yieldlab endpoint: %v", err)
	}

	uri.Path = path.Join(uri.Path, params.AdslotID)
	q := uri.Query()
	q.Set("content", "json")
	q.Set("pvid", "true")
	q.Set("ts", a.cacheBuster())
	q.Set("t", a.makeTargetingValues(params))

	if req.User != nil && req.User.BuyerUID != "" {
		q.Set("ids", "ylid:"+req.User.BuyerUID)
	}

	if req.Device != nil {
		q.Set("yl_rtb_ifa", req.Device.IFA)
		q.Set("yl_rtb_devicetype", fmt.Sprintf("%v", req.Device.DeviceType))

		if req.Device.ConnectionType != nil {
			q.Set("yl_rtb_connectiontype", fmt.Sprintf("%v", req.Device.ConnectionType.Val()))
		}

		if req.Device.Geo != nil {
			q.Set("lat", fmt.Sprintf("%v", req.Device.Geo.Lat))
			q.Set("lon", fmt.Sprintf("%v", req.Device.Geo.Lon))
		}
	}

	if req.App != nil {
		q.Set("pubappname", req.App.Name)
		q.Set("pubbundlename", req.App.Bundle)
	}

	gdpr, consent, err := a.getGDPR(req)
	if err != nil {
		return "", err
	}
	if gdpr != "" && consent != "" {
		q.Set("gdpr", gdpr)
		q.Set("consent", consent)
	}

	uri.RawQuery = q.Encode()

	return uri.String(), nil
}

func (a *YieldlabAdapter) getGDPR(request *openrtb.BidRequest) (string, string, error) {
	gdpr := ""
	var extRegs openrtb_ext.ExtRegs
	if request.Regs != nil {
		if err := json.Unmarshal(request.Regs.Ext, &extRegs); err != nil {
			return "", "", fmt.Errorf("failed to parse ExtRegs in Yieldlab GDPR check: %v", err)
		}
		if extRegs.GDPR != nil && (*extRegs.GDPR == 0 || *extRegs.GDPR == 1) {
			gdpr = strconv.Itoa(int(*extRegs.GDPR))
		}
	}

	consent := ""
	if request.User != nil && request.User.Ext != nil {
		var extUser openrtb_ext.ExtUser
		if err := json.Unmarshal(request.User.Ext, &extUser); err != nil {
			return "", "", fmt.Errorf("failed to parse ExtUser in Yieldlab GDPR check: %v", err)
		}
		consent = extUser.Consent
	}

	return gdpr, consent, nil
}

func (a *YieldlabAdapter) makeTargetingValues(params *openrtb_ext.ExtImpYieldlab) string {
	values := url.Values{}
	for k, v := range params.Targeting {
		values.Set(k, v)
	}
	return values.Encode()
}

func (a *YieldlabAdapter) makeRequest(request *openrtb.BidRequest) (*adapters.RequestData, []error) {
	params, err := a.parseRequest(request)
	if err != nil {
		return nil, []error{err}
	}

	mergedParams, err := a.mergeParams(params)
	if err != nil {
		return nil, []error{err}
	}

	bidURL, err := a.makeEndpointURL(request, mergedParams)
	if err != nil {
		return nil, []error{err}
	}

	headers := http.Header{}
	headers.Add("Accept", "application/json")
	if request.Site != nil {
		headers.Add("Referer", request.Site.Page)
	}
	if request.Device != nil {
		headers.Add("User-Agent", request.Device.UA)
		headers.Add("X-Forwarded-For", request.Device.IP)
	}
	if request.User != nil {
		headers.Add("Cookie", "id="+request.User.BuyerUID)
	}

	return &adapters.RequestData{
		Method:  "GET",
		Uri:     bidURL,
		Headers: headers,
	}, nil
}

// parseRequest extracts the Yieldlab request information from the request
func (a *YieldlabAdapter) parseRequest(request *openrtb.BidRequest) ([]*openrtb_ext.ExtImpYieldlab, error) {
	params := make([]*openrtb_ext.ExtImpYieldlab, 0)

	for i := 0; i < len(request.Imp); i++ {
		bidderExt := new(adapters.ExtImpBidder)
		if err := json.Unmarshal(request.Imp[i].Ext, bidderExt); err != nil {
			return nil, &errortypes.BadInput{
				Message: err.Error(),
			}
		}

		yieldlabExt := new(openrtb_ext.ExtImpYieldlab)
		if err := json.Unmarshal(bidderExt.Bidder, yieldlabExt); err != nil {
			return nil, &errortypes.BadInput{
				Message: err.Error(),
			}
		}

		params = append(params, yieldlabExt)
	}

	return params, nil
}

func (a *YieldlabAdapter) mergeParams(params []*openrtb_ext.ExtImpYieldlab) (*openrtb_ext.ExtImpYieldlab, error) {
	var adSlotIds []string
	targeting := make(map[string]string)

	for _, p := range params {
		adSlotIds = append(adSlotIds, p.AdslotID)
		for k, v := range p.Targeting {
			targeting[k] = v
		}
	}

	return &openrtb_ext.ExtImpYieldlab{
		AdslotID:  strings.Join(adSlotIds, adSlotIdSeparator),
		Targeting: targeting,
	}, nil
}

// MakeBids make the bids for the bid response.
func (a *YieldlabAdapter) MakeBids(internalRequest *openrtb.BidRequest, externalRequest *adapters.RequestData, response *adapters.ResponseData) (*adapters.BidderResponse, []error) {
	if response.StatusCode != 200 {
		return nil, []error{
			fmt.Errorf("failed to resolve bids from yieldlab response: Unexpected response code %v", response.StatusCode),
		}
	}

	bids := make([]*bidResponse, 0)
	if err := json.Unmarshal(response.Body, &bids); err != nil {
		return nil, []error{
			fmt.Errorf("failed to parse bids response from yieldlab: %v", err),
		}
	}

	requests, err := a.parseRequest(internalRequest)
	if err != nil {
		return nil, []error{err}
	}

	bidderResponse := &adapters.BidderResponse{
		Currency: currency.EUR.String(),
		Bids:     []*adapters.TypedBid{},
	}

	for i, bid := range bids {
		width, height, err := splitSize(bid.Adsize)
		if err != nil {
			return nil, []error{err}
		}

		req := a.findBidReq(bid.ID, requests)
		if req == nil {
			return nil, []error{
				fmt.Errorf("failed to find yieldlab request for adslotID %v. This is most likely a programming issue", bid.ID),
			}
		}

		var bidType openrtb_ext.BidType
		responseBid := &openrtb.Bid{
			ID:     strconv.FormatUint(bid.ID, 10),
			Price:  float64(bid.Price) / 100,
			ImpID:  internalRequest.Imp[i].ID,
			CrID:   a.makeCreativeID(req, bid),
			DealID: strconv.FormatUint(bid.Pid, 10),
			W:      width,
			H:      height,
		}

		if internalRequest.Imp[i].Video != nil {
			bidType = openrtb_ext.BidTypeVideo
			responseBid.NURL = a.makeAdSourceURL(internalRequest, req, bid)

		} else if internalRequest.Imp[i].Banner != nil {
			bidType = openrtb_ext.BidTypeBanner
			responseBid.AdM = a.makeBannerAdSource(internalRequest, req, bid)
		} else {
			// Yieldlab adapter currently doesn't support Audio and Native ads
			continue
		}

		bidderResponse.Bids = append(bidderResponse.Bids, &adapters.TypedBid{
			BidType: bidType,
			Bid:     responseBid,
		})
	}

	return bidderResponse, nil
}

func (a *YieldlabAdapter) findBidReq(adslotID uint64, params []*openrtb_ext.ExtImpYieldlab) *openrtb_ext.ExtImpYieldlab {
	slotIdStr := strconv.FormatUint(adslotID, 10)
	for _, p := range params {
		if p.AdslotID == slotIdStr {
			return p
		}
	}

	return nil
}

func (a *YieldlabAdapter) makeBannerAdSource(req *openrtb.BidRequest, ext *openrtb_ext.ExtImpYieldlab, res *bidResponse) string {
	return fmt.Sprintf(adSourceBanner, a.makeAdSourceURL(req, ext, res))
}

func (a *YieldlabAdapter) makeAdSourceURL(req *openrtb.BidRequest, ext *openrtb_ext.ExtImpYieldlab, res *bidResponse) string {
	val := url.Values{}
	val.Set("ts", a.cacheBuster())
	val.Set("id", ext.ExtId)
	val.Set("pvid", res.Pvid)

	if req.User != nil {
		val.Set("ids", "ylid:"+req.User.BuyerUID)
	}

	gdpr, consent, err := a.getGDPR(req)
	if err == nil && gdpr != "" && consent != "" {
		val.Set("gdpr", gdpr)
		val.Set("consent", consent)
	}

	return fmt.Sprintf(adSourceURL, ext.AdslotID, ext.SupplyID, res.Adsize, val.Encode())
}

func (a *YieldlabAdapter) makeCreativeID(req *openrtb_ext.ExtImpYieldlab, bid *bidResponse) string {
	return fmt.Sprintf(creativeID, req.AdslotID, bid.Pid, a.getWeek())
}

func splitSize(size string) (uint64, uint64, error) {
	sizeParts := strings.Split(size, adsizeSeparator)
	if len(sizeParts) != 2 {
		return 0, 0, nil
	}

	width, err := strconv.ParseUint(sizeParts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse yieldlab adsize: %v", err)
	}

	height, err := strconv.ParseUint(sizeParts[1], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse yieldlab adsize: %v", err)
	}

	return width, height, nil

}