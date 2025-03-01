// Copyright (c) 2023 Canonical Ltd
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License version 3 as
// published by the Free Software Foundation.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package daemon_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	. "gopkg.in/check.v1"

	"github.com/snapcore/snapd/overlord/state"
	"github.com/snapcore/snapd/testutil"
)

var _ = Suite(&noticesSuite{})

type noticesSuite struct {
	apiBaseSuite
}

func (s *noticesSuite) TestNoticesFilterUserID(c *C) {
	// A bit hacky... filter by user ID which doesn't have any notices to just
	// get public notices (those with nil user ID)
	s.testNoticesFilter(c, func(after time.Time) url.Values {
		return url.Values{"user-id": {"1000"}}
	})
}

func (s *noticesSuite) TestNoticesFilterType(c *C) {
	s.testNoticesFilter(c, func(after time.Time) url.Values {
		return url.Values{"types": {"change-update"}}
	})
}

func (s *noticesSuite) TestNoticesFilterKey(c *C) {
	s.testNoticesFilter(c, func(after time.Time) url.Values {
		return url.Values{"keys": {"123"}}
	})
}

func (s *noticesSuite) TestNoticesFilterAfter(c *C) {
	s.testNoticesFilter(c, func(after time.Time) url.Values {
		return url.Values{"after": {after.UTC().Format(time.RFC3339Nano)}}
	})
}

func (s *noticesSuite) TestNoticesFilterAll(c *C) {
	s.testNoticesFilter(c, func(after time.Time) url.Values {
		return url.Values{
			"user-id": {"1000"},
			"types":   {"change-update"},
			"keys":    {"123"},
			"after":   {after.UTC().Format(time.RFC3339Nano)},
		}
	})
}

func (s *noticesSuite) testNoticesFilter(c *C, makeQuery func(after time.Time) url.Values) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	uid := uint32(123)
	addNotice(c, st, &uid, state.WarningNotice, "warning", nil)
	after := time.Now()
	time.Sleep(time.Microsecond)
	noticeID, err := st.AddNotice(nil, state.ChangeUpdateNotice, "123", &state.AddNoticeOptions{
		Data: map[string]string{"k": "v"},
	})
	c.Assert(err, IsNil)
	st.Unlock()

	query := makeQuery(after)
	req, err := http.NewRequest("GET", "/v2/notices?"+query.Encode(), nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=0;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 1)
	n := noticeToMap(c, notices[0])

	firstOccurred, err := time.Parse(time.RFC3339, n["first-occurred"].(string))
	c.Assert(err, IsNil)
	c.Assert(firstOccurred.After(after), Equals, true)
	lastOccurred, err := time.Parse(time.RFC3339, n["last-occurred"].(string))
	c.Assert(err, IsNil)
	c.Assert(lastOccurred.Equal(firstOccurred), Equals, true)
	lastRepeated, err := time.Parse(time.RFC3339, n["last-repeated"].(string))
	c.Assert(err, IsNil)
	c.Assert(lastRepeated.Equal(firstOccurred), Equals, true)

	delete(n, "first-occurred")
	delete(n, "last-occurred")
	delete(n, "last-repeated")
	c.Assert(n, DeepEquals, map[string]any{
		"id":           noticeID,
		"user-id":      nil,
		"type":         "change-update",
		"key":          "123",
		"occurrences":  1.0,
		"last-data":    map[string]any{"k": "v"},
		"expire-after": "168h0m0s",
	})
}

func (s *noticesSuite) TestNoticesFilterMultipleTypes(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	addNotice(c, st, nil, state.ChangeUpdateNotice, "123", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	req, err := http.NewRequest("GET", "/v2/notices?types=change-update&types=warning", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 2)
	n := noticeToMap(c, notices[0])
	c.Assert(n["type"], Equals, "change-update")
	n = noticeToMap(c, notices[1])
	c.Assert(n["type"], Equals, "warning")
}

func (s *noticesSuite) TestNoticesFilterMultipleKeys(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	addNotice(c, st, nil, state.ChangeUpdateNotice, "123", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.ChangeUpdateNotice, "456", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	req, err := http.NewRequest("GET", "/v2/notices?keys=456&keys=danger", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 2)
	n := noticeToMap(c, notices[0])
	c.Assert(n["key"], Equals, "456")
	n = noticeToMap(c, notices[1])
	c.Assert(n["key"], Equals, "danger")
}

func (s *noticesSuite) TestNoticesFilterInvalidTypes(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	addNotice(c, st, nil, state.ChangeUpdateNotice, "123", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	// Check that invalid types are discarded, and notices with remaining
	// types are requested as expected, without error.
	req, err := http.NewRequest("GET", "/v2/notices?types=foo&types=warning&types=bar,baz", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 1)
	n := noticeToMap(c, notices[0])
	c.Assert(n["type"], Equals, "warning")

	// Check that if all types are invalid, no notices are returned, and there
	// is no error.
	req, err = http.NewRequest("GET", "/v2/notices?types=foo&types=bar,baz", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp = s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok = rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 0)
}

func (s *noticesSuite) TestNoticesUserIDAdminDefault(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	admin := uint32(0)
	nonAdmin := uint32(1000)
	otherNonAdmin := uint32(123)
	addNotice(c, st, &admin, state.ChangeUpdateNotice, "123", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &nonAdmin, state.WarningNotice, "error1", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &otherNonAdmin, state.ChangeUpdateNotice, "456", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	// Test that admin user sees their own and all public notices if no filter is specified
	req, err := http.NewRequest("GET", "/v2/notices", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=0;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 2)
	n := noticeToMap(c, notices[0])
	c.Assert(n["user-id"], Equals, float64(admin))
	c.Assert(n["key"], Equals, "123")
	n = noticeToMap(c, notices[1])
	c.Assert(n["user-id"], Equals, nil)
	c.Assert(n["key"], Equals, "danger")
}

func (s *noticesSuite) TestNoticesUserIDAdminFilter(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	admin := uint32(0)
	nonAdmin := uint32(1000)
	otherNonAdmin := uint32(123)
	addNotice(c, st, &admin, state.ChangeUpdateNotice, "123", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &nonAdmin, state.WarningNotice, "error1", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &otherNonAdmin, state.ChangeUpdateNotice, "456", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	// Test that admin can filter on any user ID, and always gets public notices too
	for _, uid := range []uint32{0, 1000, 123} {
		userIDValues := url.Values{}
		userIDValues.Add("user-id", strconv.FormatUint(uint64(uid), 10))
		reqUrl := fmt.Sprintf("/v2/notices?%s", userIDValues.Encode())
		req, err := http.NewRequest("GET", reqUrl, nil)
		c.Assert(err, IsNil)
		req.RemoteAddr = "pid=100;uid=0;socket=;"
		rsp := s.syncReq(c, req, nil)
		c.Check(rsp.Status, Equals, 200)

		notices, ok := rsp.Result.([]*state.Notice)
		c.Assert(ok, Equals, true)
		c.Assert(notices, HasLen, 2)
		n := noticeToMap(c, notices[0])
		c.Assert(n["user-id"], Equals, float64(uid))
		n = noticeToMap(c, notices[1])
		c.Assert(n["user-id"], Equals, nil)
	}
}

func (s *noticesSuite) TestNoticesUserIDNonAdminDefault(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	admin := uint32(0)
	nonAdmin := uint32(1000)
	otherNonAdmin := uint32(123)
	addNotice(c, st, &admin, state.ChangeUpdateNotice, "123", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &nonAdmin, state.WarningNotice, "error1", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &otherNonAdmin, state.ChangeUpdateNotice, "456", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	// Test that non-admin user by default only sees their notices and public notices.
	req, err := http.NewRequest("GET", "/v2/notices", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 2)
	n := noticeToMap(c, notices[0])
	c.Assert(n["user-id"], Equals, float64(nonAdmin))
	c.Assert(n["key"], Equals, "error1")
	n = noticeToMap(c, notices[1])
	c.Assert(n["user-id"], Equals, nil)
	c.Assert(n["key"], Equals, "danger")
}

func (s *noticesSuite) TestNoticesUserIDNonAdminFilter(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	nonAdmin := uint32(1000)
	addNotice(c, st, &nonAdmin, state.WarningNotice, "error1", nil)
	st.Unlock()

	// Test that non-admin user may not use --user-id filter
	reqUrl := "/v2/notices?user-id=1000"
	req, err := http.NewRequest("GET", reqUrl, nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 403)
}

func (s *noticesSuite) TestNoticesUsersAdminFilter(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	admin := uint32(0)
	nonAdmin := uint32(1000)
	otherNonAdmin := uint32(123)
	addNotice(c, st, &admin, state.ChangeUpdateNotice, "123", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &nonAdmin, state.WarningNotice, "error1", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, &otherNonAdmin, state.ChangeUpdateNotice, "456", nil)
	time.Sleep(time.Microsecond)
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	// Test that admin user may get all notices with --users=all filter
	reqUrl := "/v2/notices?users=all"
	req, err := http.NewRequest("GET", reqUrl, nil)
	c.Check(err, IsNil)
	req.RemoteAddr = "pid=100;uid=0;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 4)
	n := noticeToMap(c, notices[0])
	c.Assert(n["user-id"], Equals, float64(admin))
	c.Assert(n["key"], Equals, "123")
	n = noticeToMap(c, notices[1])
	c.Assert(n["user-id"], Equals, float64(nonAdmin))
	c.Assert(n["key"], Equals, "error1")
	n = noticeToMap(c, notices[2])
	c.Assert(n["user-id"], Equals, float64(otherNonAdmin))
	c.Assert(n["key"], Equals, "456")
	n = noticeToMap(c, notices[3])
	c.Assert(n["user-id"], Equals, nil)
	c.Assert(n["key"], Equals, "danger")
}

func (s *noticesSuite) TestNoticesUsersNonAdminFilter(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	nonAdmin := uint32(1000)
	addNotice(c, st, &nonAdmin, state.WarningNotice, "error1", nil)
	st.Unlock()

	// Test that non-admin user may not use --users filter
	reqUrl := "/v2/notices?users=all"
	req, err := http.NewRequest("GET", reqUrl, nil)
	c.Check(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 403)
}

func (s *noticesSuite) TestNoticesUnknownRequestUID(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	addNotice(c, st, nil, state.WarningNotice, "danger", nil)
	st.Unlock()

	// Test that a connection with unknown UID is forbidden from receiving notices
	req, err := http.NewRequest("GET", "/v2/notices", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 403)
}

func (s *noticesSuite) TestNoticesWait(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	go func() {
		time.Sleep(testutil.HostScaledTimeout(50 * time.Millisecond))
		st.Lock()
		addNotice(c, st, nil, state.WarningNotice, "foo", nil)
		st.Unlock()
	}()

	timeout := testutil.HostScaledTimeout(5 * time.Second).String()
	req, err := http.NewRequest("GET", "/v2/notices?timeout="+timeout, nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 1)
	n := noticeToMap(c, notices[0])
	c.Check(n["user-id"], Equals, nil)
	c.Check(n["type"], Equals, "warning")
	c.Check(n["key"], Equals, "foo")
}

func (s *noticesSuite) TestNoticesTimeout(c *C) {
	s.daemon(c)

	req, err := http.NewRequest("GET", "/v2/notices?timeout=1ms", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notices, ok := rsp.Result.([]*state.Notice)
	c.Assert(ok, Equals, true)
	c.Assert(notices, HasLen, 0)
}

func (s *noticesSuite) TestNoticesRequestCancelled(c *C) {
	s.daemon(c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelTimeout := testutil.HostScaledTimeout(50 * time.Millisecond)
	reqTimeout := testutil.HostScaledTimeout(5 * time.Second)

	start := time.Now()

	go func() {
		time.Sleep(cancelTimeout)
		cancel()
	}()

	req, err := http.NewRequestWithContext(ctx, "GET", "/v2/notices?timeout="+reqTimeout.String(), nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 400)
	c.Check(rsp.Message, Matches, "request canceled")

	elapsed := time.Since(start)
	c.Check(elapsed > cancelTimeout, Equals, true)
	c.Check(elapsed < reqTimeout, Equals, true)
}

func (s *noticesSuite) TestNoticesInvalidUserID(c *C) {
	s.testNoticesBadRequest(c, "user-id=foo", `invalid "user-id" filter:.*`)
}

func (s *noticesSuite) TestNoticesInvalidUserIDMultiple(c *C) {
	s.testNoticesBadRequest(c, "user-id=1000&user-id=1234", `invalid "user-id" filter:.*`)
}

func (s *noticesSuite) TestNoticesInvalidUserIDHigh(c *C) {
	s.testNoticesBadRequest(c, "user-id=4294967296", `invalid "user-id" filter:.*`)
}

func (s *noticesSuite) TestNoticesInvalidUserIDLow(c *C) {
	s.testNoticesBadRequest(c, "user-id=-1", `invalid "user-id" filter:.*`)
}

func (s *noticesSuite) TestNoticesInvalidUsers(c *C) {
	s.testNoticesBadRequest(c, "users=foo", `invalid "users" filter:.*`)
}

func (s *noticesSuite) TestNoticesInvalidUserIDWithUsers(c *C) {
	s.testNoticesBadRequest(c, "user-id=1234&users=all", `cannot use both "users" and "user-id" parameters`)
}

func (s *noticesSuite) TestNoticesInvalidAfter(c *C) {
	s.testNoticesBadRequest(c, "after=foo", `invalid "after" timestamp.*`)
}

func (s *noticesSuite) TestNoticesInvalidTimeout(c *C) {
	s.testNoticesBadRequest(c, "timeout=foo", "invalid timeout.*")
}

func (s *noticesSuite) testNoticesBadRequest(c *C, query, errorMatch string) {
	s.daemon(c)

	req, err := http.NewRequest("GET", "/v2/notices?"+query, nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=0;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 400)
	c.Assert(rsp.Message, Matches, errorMatch)
}

func (s *noticesSuite) TestNotice(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	addNotice(c, st, nil, state.WarningNotice, "foo", nil)
	noticeIDPublic, err := st.AddNotice(nil, state.WarningNotice, "bar", nil)
	c.Assert(err, IsNil)
	uid := uint32(1000)
	noticeIDPrivate, err := st.AddNotice(&uid, state.WarningNotice, "fizz", nil)
	c.Assert(err, IsNil)
	addNotice(c, st, &uid, state.WarningNotice, "baz", nil)
	st.Unlock()

	req, err := http.NewRequest("GET", "/v2/notices/"+noticeIDPublic, nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notice, ok := rsp.Result.(*state.Notice)
	c.Assert(ok, Equals, true)
	n := noticeToMap(c, notice)
	c.Check(n["user-id"], Equals, nil)
	c.Check(n["type"], Equals, "warning")
	c.Check(n["key"], Equals, "bar")

	req, err = http.NewRequest("GET", "/v2/notices/"+noticeIDPrivate, nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp = s.syncReq(c, req, nil)
	c.Check(rsp.Status, Equals, 200)

	notice, ok = rsp.Result.(*state.Notice)
	c.Assert(ok, Equals, true)
	n = noticeToMap(c, notice)
	c.Check(n["user-id"], Equals, 1000.0)
	c.Check(n["type"], Equals, "warning")
	c.Check(n["key"], Equals, "fizz")
}

func (s *noticesSuite) TestNoticeNotFound(c *C) {
	s.daemon(c)

	req, err := http.NewRequest("GET", "/v2/notices/1234", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1000;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 404)
}

func (s *noticesSuite) TestNoticeUnknownRequestUID(c *C) {
	s.daemon(c)

	req, err := http.NewRequest("GET", "/v2/notices/1234", nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 403)
}

func (s *noticesSuite) TestNoticeAdminAllowed(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	uid := uint32(1000)
	noticeID, err := st.AddNotice(&uid, state.WarningNotice, "danger", nil)
	c.Assert(err, IsNil)
	st.Unlock()

	req, err := http.NewRequest("GET", "/v2/notices/"+noticeID, nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=0;socket=;"
	rsp := s.syncReq(c, req, nil)
	c.Assert(rsp.Status, Equals, 200)

	notice, ok := rsp.Result.(*state.Notice)
	c.Assert(ok, Equals, true)
	n := noticeToMap(c, notice)
	c.Check(n["user-id"], Equals, 1000.0)
	c.Check(n["type"], Equals, "warning")
	c.Check(n["key"], Equals, "danger")
}

func (s *noticesSuite) TestNoticeNonAdminNotAllowed(c *C) {
	s.daemon(c)

	st := s.d.Overlord().State()
	st.Lock()
	uid := uint32(1000)
	noticeID, err := st.AddNotice(&uid, state.WarningNotice, "danger", nil)
	c.Assert(err, IsNil)
	st.Unlock()

	req, err := http.NewRequest("GET", "/v2/notices/"+noticeID, nil)
	c.Assert(err, IsNil)
	req.RemoteAddr = "pid=100;uid=1001;socket=;"
	rsp := s.errorReq(c, req, nil)
	c.Check(rsp.Status, Equals, 403)
}

func noticeToMap(c *C, notice *state.Notice) map[string]any {
	buf, err := json.Marshal(notice)
	c.Assert(err, IsNil)
	var n map[string]any
	err = json.Unmarshal(buf, &n)
	c.Assert(err, IsNil)
	return n
}

func addNotice(c *C, st *state.State, userID *uint32, noticeType state.NoticeType, key string, options *state.AddNoticeOptions) {
	_, err := st.AddNotice(userID, noticeType, key, options)
	c.Assert(err, IsNil)
}
