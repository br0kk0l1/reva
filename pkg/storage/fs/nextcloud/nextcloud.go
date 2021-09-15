// Copyright 2018-2021 CERN
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// In applying this license, CERN does not waive the privileges and immunities
// granted to it by virtue of its status as an Intergovernmental Organization
// or submit itself to any jurisdiction.

package nextcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	user "github.com/cs3org/go-cs3apis/cs3/identity/user/v1beta1"
	provider "github.com/cs3org/go-cs3apis/cs3/storage/provider/v1beta1"
	types "github.com/cs3org/go-cs3apis/cs3/types/v1beta1"
	"github.com/cs3org/reva/pkg/appctx"
	ctxpkg "github.com/cs3org/reva/pkg/ctx"
	"github.com/cs3org/reva/pkg/errtypes"
	"github.com/cs3org/reva/pkg/storage"
	"github.com/cs3org/reva/pkg/storage/fs/registry"
	"github.com/mitchellh/mapstructure"
	"github.com/pkg/errors"
)

func init() {
	registry.Register("nextcloud", New)
}

// StorageDriverConfig is the configuration struct for a NextcloudStorageDriver
type StorageDriverConfig struct {
	EndPoint string `mapstructure:"end_point"` // e.g. "http://nc/apps/sciencemesh/~alice/"
	MockHTTP bool   `mapstructure:"mock_http"`
}

// StorageDriver implements the storage.FS interface
// and connects with a StorageDriver server as its backend
type StorageDriver struct {
	endPoint string
	client   *http.Client
}

func parseConfig(m map[string]interface{}) (*StorageDriverConfig, error) {
	c := &StorageDriverConfig{}
	if err := mapstructure.Decode(m, c); err != nil {
		err = errors.Wrap(err, "error decoding conf")
		return nil, err
	}
	return c, nil
}

// New returns an implementation to of the storage.FS interface that talks to
// a Nextcloud instance over http.
func New(m map[string]interface{}) (storage.FS, error) {
	conf, err := parseConfig(m)
	if err != nil {
		return nil, err
	}

	return NewStorageDriver(conf)
}

// CreateStorageSpace creates a storage space
func (nc *StorageDriver) CreateStorageSpace(ctx context.Context, req *provider.CreateStorageSpaceRequest) (*provider.CreateStorageSpaceResponse, error) {
	return nil, fmt.Errorf("unimplemented: CreateStorageSpace")
}

// NewStorageDriver returns a new NextcloudStorageDriver
func NewStorageDriver(c *StorageDriverConfig) (*StorageDriver, error) {
	var client *http.Client
	if c.MockHTTP {
		called := make([]string, 0)
		nextcloudServerMock := GetNextcloudServerMock(&called)
		client, _ = TestingHTTPClient(nextcloudServerMock)
	} else {
		client = &http.Client{}
	}
	return &StorageDriver{
		endPoint: c.EndPoint, // e.g. "http://nc/apps/sciencemesh/"
		client:   client,
	}, nil
}

// Action describes a REST request to forward to the Nextcloud backend
type Action struct {
	verb string
	argS string
}

func getUser(ctx context.Context) (*user.User, error) {
	u, ok := ctxpkg.ContextGetUser(ctx)
	if !ok {
		err := errors.Wrap(errtypes.UserRequired(""), "nextcloud storage driver: error getting user from ctx")
		return nil, err
	}
	return u, nil
}

// SetHTTPClient sets the HTTP client
func (nc *StorageDriver) SetHTTPClient(c *http.Client) {
	nc.client = c
}

func (nc *StorageDriver) doUpload(ctx context.Context, filePath string, r io.ReadCloser) error {
	// log := appctx.GetLogger(ctx)
	user, err := getUser(ctx)
	if err != nil {
		return err
	}
	// See https://github.com/pondersource/nc-sciencemesh/issues/5
	// url := nc.endPoint + "~" + user.Username + "/files/" + filePath
	url := nc.endPoint + "~" + user.Username + "/api/Upload/" + filePath
	req, err := http.NewRequest(http.MethodPut, url, r)
	if err != nil {
		panic(err)
	}

	// set the request header Content-Type for the upload
	// FIXME: get the actual content type from somewhere
	req.Header.Set("Content-Type", "text/plain")
	resp, err := nc.client.Do(req)
	if err != nil {
		panic(err)
	}

	defer resp.Body.Close()
	_, err = io.ReadAll(resp.Body)
	return err
}

func (nc *StorageDriver) doDownload(ctx context.Context, filePath string) (io.ReadCloser, error) {
	user, err := getUser(ctx)
	if err != nil {
		return nil, err
	}
	// See https://github.com/pondersource/nc-sciencemesh/issues/5
	// url := nc.endPoint + "~" + user.Username + "/files/" + filePath
	url := nc.endPoint + "~" + user.Username + "/api/Download/" + filePath
	req, err := http.NewRequest(http.MethodGet, url, strings.NewReader(""))
	if err != nil {
		panic(err)
	}

	resp, err := nc.client.Do(req)
	if err != nil {
		panic(err)
	}
	if resp.StatusCode != 200 {
		panic("No 200 response code in download request")
	}

	return resp.Body, err
}

func (nc *StorageDriver) doDownloadRevision(ctx context.Context, filePath string, key string) (io.ReadCloser, error) {
	user, err := getUser(ctx)
	if err != nil {
		return nil, err
	}
	// See https://github.com/pondersource/nc-sciencemesh/issues/5
	url := nc.endPoint + "~" + user.Username + "/api/DownloadRevision/" + url.QueryEscape(key) + "/" + filePath
	req, err := http.NewRequest(http.MethodGet, url, strings.NewReader(""))
	if err != nil {
		panic(err)
	}

	resp, err := nc.client.Do(req)
	if err != nil {
		panic(err)
	}
	if resp.StatusCode != 200 {
		panic("No 200 response code in download request")
	}

	return resp.Body, err
}

func (nc *StorageDriver) do(ctx context.Context, a Action) (int, []byte, error) {
	log := appctx.GetLogger(ctx)
	user, err := getUser(ctx)
	if err != nil {
		return 0, nil, err
	}
	url := nc.endPoint + "~" + user.Username + "/api/" + a.verb
	log.Info().Msgf("nc.do %s", url)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(a.argS))
	if err != nil {
		return 0, nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := nc.client.Do(req)
	if err != nil {
		return 0, nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, err
	}

	fmt.Printf("nc.do response %d %s\n", resp.StatusCode, body)
	return resp.StatusCode, body, nil
}

// GetHome as defined in the storage.FS interface
func (nc *StorageDriver) GetHome(ctx context.Context) (string, error) {
	log := appctx.GetLogger(ctx)
	log.Info().Msg("GetHome")

	_, respBody, err := nc.do(ctx, Action{"GetHome", ""})
	return string(respBody), err
}

// CreateHome as defined in the storage.FS interface
func (nc *StorageDriver) CreateHome(ctx context.Context) error {
	log := appctx.GetLogger(ctx)
	log.Info().Msg("CreateHome")

	_, _, err := nc.do(ctx, Action{"CreateHome", ""})
	return err
}

// CreateDir as defined in the storage.FS interface
func (nc *StorageDriver) CreateDir(ctx context.Context, ref *provider.Reference) error {
	bodyStr, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("CreateDir %s", bodyStr)

	_, _, err = nc.do(ctx, Action{"CreateDir", string(bodyStr)})
	return err
}

// Delete as defined in the storage.FS interface
func (nc *StorageDriver) Delete(ctx context.Context, ref *provider.Reference) error {
	bodyStr, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("Delete %s", bodyStr)

	_, _, err = nc.do(ctx, Action{"Delete", string(bodyStr)})
	return err
}

// Move as defined in the storage.FS interface
func (nc *StorageDriver) Move(ctx context.Context, oldRef, newRef *provider.Reference) error {
	type paramsObj struct {
		OldRef provider.Reference `json:"oldRef"`
		NewRef provider.Reference `json:"newRef"`
	}
	bodyObj := &paramsObj{
		OldRef: *oldRef,
		NewRef: *newRef,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("Move %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"Move", string(bodyStr)})
	return err
}

// GetMD as defined in the storage.FS interface
func (nc *StorageDriver) GetMD(ctx context.Context, ref *provider.Reference, mdKeys []string) (*provider.ResourceInfo, error) {
	type paramsObj struct {
		Ref    provider.Reference `json:"ref"`
		MdKeys []string           `json:"mdKeys"`
		// MetaData provider.ResourceInfo `json:"metaData"`
	}
	bodyObj := &paramsObj{
		Ref:    *ref,
		MdKeys: mdKeys,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("GetMD %s", bodyStr)

	status, body, err := nc.do(ctx, Action{"GetMD", string(bodyStr)})
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, errtypes.NotFound("")
	}
	var respObj provider.ResourceInfo
	err = json.Unmarshal(body, &respObj)
	if err != nil {
		return nil, err
	}
	return &respObj, nil
}

// ListFolder as defined in the storage.FS interface
func (nc *StorageDriver) ListFolder(ctx context.Context, ref *provider.Reference, mdKeys []string) ([]*provider.ResourceInfo, error) {
	type paramsObj struct {
		Ref    provider.Reference `json:"ref"`
		MdKeys []string           `json:"mdKeys"`
	}
	bodyObj := &paramsObj{
		Ref:    *ref,
		MdKeys: mdKeys,
	}
	bodyStr, err := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("LisfFolder %s", bodyStr)
	if err != nil {
		return nil, err
	}
	status, body, err := nc.do(ctx, Action{"ListFolder", string(bodyStr)})
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, errtypes.NotFound("")
	}

	var respMapArr []provider.ResourceInfo
	err = json.Unmarshal(body, &respMapArr)
	if err != nil {
		return nil, err
	}
	var pointers = make([]*provider.ResourceInfo, len(respMapArr))
	for i := 0; i < len(respMapArr); i++ {
		pointers[i] = &respMapArr[i]
	}
	return pointers, err
}

// InitiateUpload as defined in the storage.FS interface
func (nc *StorageDriver) InitiateUpload(ctx context.Context, ref *provider.Reference, uploadLength int64, metadata map[string]string) (map[string]string, error) {
	type paramsObj struct {
		Ref          provider.Reference `json:"ref"`
		UploadLength int64              `json:"uploadLength"`
		Metadata     map[string]string  `json:"metadata"`
	}
	bodyObj := &paramsObj{
		Ref:          *ref,
		UploadLength: uploadLength,
		Metadata:     metadata,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("InitiateUpload %s", bodyStr)

	_, respBody, err := nc.do(ctx, Action{"InitiateUpload", string(bodyStr)})
	if err != nil {
		return nil, err
	}
	respMap := make(map[string]string)
	err = json.Unmarshal(respBody, &respMap)
	if err != nil {
		return nil, err
	}
	return respMap, err
}

// Upload as defined in the storage.FS interface
func (nc *StorageDriver) Upload(ctx context.Context, ref *provider.Reference, r io.ReadCloser) error {
	return nc.doUpload(ctx, ref.Path, r)
}

// Download as defined in the storage.FS interface
func (nc *StorageDriver) Download(ctx context.Context, ref *provider.Reference) (io.ReadCloser, error) {
	return nc.doDownload(ctx, ref.Path)
}

// ListRevisions as defined in the storage.FS interface
func (nc *StorageDriver) ListRevisions(ctx context.Context, ref *provider.Reference) ([]*provider.FileVersion, error) {
	bodyStr, _ := json.Marshal(ref)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("ListRevisions %s", bodyStr)

	_, respBody, err := nc.do(ctx, Action{"ListRevisions", string(bodyStr)})
	// fmt.Printf("ListRevisions respBody %s", respBody)

	if err != nil {
		return nil, err
	}
	var respMapArr []provider.FileVersion
	err = json.Unmarshal(respBody, &respMapArr)
	if err != nil {
		return nil, err
	}
	revs := make([]*provider.FileVersion, len(respMapArr))
	for i := 0; i < len(respMapArr); i++ {
		revs[i] = &respMapArr[i]
	}
	return revs, err
}

// DownloadRevision as defined in the storage.FS interface
func (nc *StorageDriver) DownloadRevision(ctx context.Context, ref *provider.Reference, key string) (io.ReadCloser, error) {
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("DownloadRevision %s %s", ref.Path, key)

	readCloser, err := nc.doDownloadRevision(ctx, ref.Path, key)
	return readCloser, err
}

// RestoreRevision as defined in the storage.FS interface
func (nc *StorageDriver) RestoreRevision(ctx context.Context, ref *provider.Reference, key string) error {
	type paramsObj struct {
		Ref provider.Reference `json:"ref"`
		Key string             `json:"key"`
	}
	bodyObj := &paramsObj{
		Ref: *ref,
		Key: key,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("RestoreRevision %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"RestoreRevision", string(bodyStr)})
	return err
}

// ListRecycle as defined in the storage.FS interface
func (nc *StorageDriver) ListRecycle(ctx context.Context, key string, path string) ([]*provider.RecycleItem, error) {
	log := appctx.GetLogger(ctx)
	log.Info().Msg("ListRecycle")
	type paramsObj struct {
		Key  string `json:"key"`
		Path string `json:"path"`
	}
	bodyObj := &paramsObj{
		Key:  key,
		Path: path,
	}
	bodyStr, _ := json.Marshal(bodyObj)

	_, respBody, err := nc.do(ctx, Action{"ListRecycle", string(bodyStr)})

	if err != nil {
		return nil, err
	}
	var respMapArr []provider.RecycleItem
	err = json.Unmarshal(respBody, &respMapArr)
	if err != nil {
		return nil, err
	}
	items := make([]*provider.RecycleItem, len(respMapArr))
	for i := 0; i < len(respMapArr); i++ {
		items[i] = &respMapArr[i]
	}
	return items, err
}

// RestoreRecycleItem as defined in the storage.FS interface
func (nc *StorageDriver) RestoreRecycleItem(ctx context.Context, key string, path string, restoreRef *provider.Reference) error {
	type paramsObj struct {
		Key        string             `json:"key"`
		Path       string             `json:"path"`
		RestoreRef provider.Reference `json:"restoreRef"`
	}
	bodyObj := &paramsObj{
		Key:        key,
		Path:       path,
		RestoreRef: *restoreRef,
	}
	bodyStr, _ := json.Marshal(bodyObj)

	log := appctx.GetLogger(ctx)
	log.Info().Msgf("RestoreRecycleItem %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"RestoreRecycleItem", string(bodyStr)})
	return err
}

// PurgeRecycleItem as defined in the storage.FS interface
func (nc *StorageDriver) PurgeRecycleItem(ctx context.Context, key string, path string) error {
	type paramsObj struct {
		Key  string `json:"key"`
		Path string `json:"path"`
	}
	bodyObj := &paramsObj{
		Key:  key,
		Path: path,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("PurgeRecycleItem %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"PurgeRecycleItem", string(bodyStr)})
	return err
}

// EmptyRecycle as defined in the storage.FS interface
func (nc *StorageDriver) EmptyRecycle(ctx context.Context) error {
	log := appctx.GetLogger(ctx)
	log.Info().Msg("EmptyRecycle")

	_, _, err := nc.do(ctx, Action{"EmptyRecycle", ""})
	return err
}

// GetPathByID as defined in the storage.FS interface
func (nc *StorageDriver) GetPathByID(ctx context.Context, id *provider.ResourceId) (string, error) {
	bodyStr, _ := json.Marshal(id)
	_, respBody, err := nc.do(ctx, Action{"GetPathByID", string(bodyStr)})
	return string(respBody), err
}

// AddGrant as defined in the storage.FS interface
func (nc *StorageDriver) AddGrant(ctx context.Context, ref *provider.Reference, g *provider.Grant) error {
	type paramsObj struct {
		Ref provider.Reference `json:"ref"`
		G   provider.Grant     `json:"g"`
	}
	bodyObj := &paramsObj{
		Ref: *ref,
		G:   *g,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("AddGrant %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"AddGrant", string(bodyStr)})
	return err
}

// RemoveGrant as defined in the storage.FS interface
func (nc *StorageDriver) RemoveGrant(ctx context.Context, ref *provider.Reference, g *provider.Grant) error {
	type paramsObj struct {
		Ref provider.Reference `json:"ref"`
		G   provider.Grant     `json:"g"`
	}
	bodyObj := &paramsObj{
		Ref: *ref,
		G:   *g,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("RemoveGrant %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"RemoveGrant", string(bodyStr)})
	return err
}

// DenyGrant as defined in the storage.FS interface
func (nc *StorageDriver) DenyGrant(ctx context.Context, ref *provider.Reference, g *provider.Grantee) error {
	type paramsObj struct {
		Ref provider.Reference `json:"ref"`
		G   provider.Grantee   `json:"g"`
	}
	bodyObj := &paramsObj{
		Ref: *ref,
		G:   *g,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("DenyGrant %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"DenyGrant", string(bodyStr)})
	return err
}

// UpdateGrant as defined in the storage.FS interface
func (nc *StorageDriver) UpdateGrant(ctx context.Context, ref *provider.Reference, g *provider.Grant) error {
	type paramsObj struct {
		Ref provider.Reference `json:"ref"`
		G   provider.Grant     `json:"g"`
	}
	bodyObj := &paramsObj{
		Ref: *ref,
		G:   *g,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("UpdateGrant %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"UpdateGrant", string(bodyStr)})
	return err
}

// ListGrants as defined in the storage.FS interface
func (nc *StorageDriver) ListGrants(ctx context.Context, ref *provider.Reference) ([]*provider.Grant, error) {
	bodyStr, _ := json.Marshal(provider.Grant{
		Grantee: &provider.Grantee{
			Id: &provider.Grantee_UserId{
				UserId: &user.UserId{
					Idp:      "some-idp",
					OpaqueId: "some-opaque-id",
					Type:     user.UserType_USER_TYPE_PRIMARY,
				},
			},
		},
		Permissions: &provider.ResourcePermissions{
			AddGrant:             true,
			CreateContainer:      true,
			Delete:               true,
			GetPath:              true,
			GetQuota:             true,
			InitiateFileDownload: true,
			InitiateFileUpload:   true,
			ListGrants:           true,
			ListContainer:        false,
			ListFileVersions:     false,
			ListRecycle:          false,
			Move:                 false,
			RemoveGrant:          false,
			PurgeRecycle:         false,
			RestoreFileVersion:   false,
			RestoreRecycleItem:   false,
			Stat:                 false,
			UpdateGrant:          false,
			XXX_NoUnkeyedLiteral: struct{}{},
			XXX_unrecognized:     []byte{},
			XXX_sizecache:        0,
		},
		XXX_NoUnkeyedLiteral: struct{}{},
		XXX_unrecognized:     []byte{},
		XXX_sizecache:        0,
	},
	)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("ListGrants %s", bodyStr)

	_, respBody, err := nc.do(ctx, Action{"ListGrants", string(bodyStr)})
	if err != nil {
		return nil, err
	}

	var respMapArr []provider.Grant
	err = json.Unmarshal(respBody, &respMapArr)
	if err != nil {
		return nil, err
	}
	grants := make([]*provider.Grant, len(respMapArr))
	for i := 0; i < len(respMapArr); i++ {
		grants[i] = &respMapArr[i]
	}
	return grants, err
}

// GetQuota as defined in the storage.FS interface
func (nc *StorageDriver) GetQuota(ctx context.Context) (uint64, uint64, error) {
	log := appctx.GetLogger(ctx)
	log.Info().Msg("GetQuota")

	_, respBody, err := nc.do(ctx, Action{"GetQuota", ""})
	if err != nil {
		return 0, 0, err
	}

	var respMap map[string]interface{}
	err = json.Unmarshal(respBody, &respMap)
	if err != nil {
		return 0, 0, err
	}
	return uint64(respMap["maxBytes"].(float64)), uint64(respMap["maxFiles"].(float64)), err
}

// CreateReference as defined in the storage.FS interface
func (nc *StorageDriver) CreateReference(ctx context.Context, path string, targetURI *url.URL) error {
	type paramsObj struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	bodyObj := &paramsObj{
		Path: path,
		URL:  targetURI.String(),
	}
	bodyStr, _ := json.Marshal(bodyObj)

	_, _, err := nc.do(ctx, Action{"CreateReference", string(bodyStr)})
	return err
}

// Shutdown as defined in the storage.FS interface
func (nc *StorageDriver) Shutdown(ctx context.Context) error {
	log := appctx.GetLogger(ctx)
	log.Info().Msg("Shutdown")

	_, _, err := nc.do(ctx, Action{"Shutdown", ""})
	return err
}

// SetArbitraryMetadata as defined in the storage.FS interface
func (nc *StorageDriver) SetArbitraryMetadata(ctx context.Context, ref *provider.Reference, md *provider.ArbitraryMetadata) error {
	type paramsObj struct {
		Reference provider.Reference         `json:"reference"`
		Metadata  provider.ArbitraryMetadata `json:"metadata"`
	}
	bodyObj := &paramsObj{
		Reference: *ref,
		Metadata:  *md,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("SetArbitraryMetadata %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"SetArbitraryMetadata", string(bodyStr)})
	return err
}

// UnsetArbitraryMetadata as defined in the storage.FS interface
func (nc *StorageDriver) UnsetArbitraryMetadata(ctx context.Context, ref *provider.Reference, keys []string) error {
	type paramsObj struct {
		Reference provider.Reference `json:"reference"`
		Keys      []string           `json:"keys"`
	}
	bodyObj := &paramsObj{
		Reference: *ref,
		Keys:      keys,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	log := appctx.GetLogger(ctx)
	log.Info().Msgf("UnsetArbitraryMetadata %s", bodyStr)

	_, _, err := nc.do(ctx, Action{"UnsetArbitraryMetadata", string(bodyStr)})
	return err
}

// ListStorageSpaces :as defined in the storage.FS interface
func (nc *StorageDriver) ListStorageSpaces(ctx context.Context, f []*provider.ListStorageSpacesRequest_Filter) ([]*provider.StorageSpace, error) {
	type paramsObj struct {
		Filters []*provider.ListStorageSpacesRequest_Filter `json:"filters"`
	}
	bodyObj := &paramsObj{
		Filters: f,
	}
	bodyStr, _ := json.Marshal(bodyObj)
	_, respBody, err := nc.do(ctx, Action{"ListStorageSpaces", string(bodyStr)})
	if err != nil {
		return nil, err
	}

	// https://github.com/cs3org/go-cs3apis/blob/970eec3/cs3/storage/provider/v1beta1/resources.pb.go#L1341-L1366
	var respMapArr []interface{}
	err = json.Unmarshal(respBody, &respMapArr)
	if err != nil {
		return nil, err
	}
	var spaces = make([]*provider.StorageSpace, len(respMapArr))
	for i := 0; i < len(respMapArr); i++ {
		respMap := respMapArr[i].(map[string]interface{})
		opaqueMap := make(map[string](*types.OpaqueEntry))
		values := respMap["opaque"].(map[string]interface{})
		for k, v := range values {
			opaqueMap[k] = &types.OpaqueEntry{Value: []byte(v.(string))}
		}
		spaces[i] = &provider.StorageSpace{
			Opaque: &types.Opaque{Map: opaqueMap},
			Id:     &provider.StorageSpaceId{OpaqueId: respMap["opaqueId"].(string)},
			Owner: &user.User{
				Id: &user.UserId{
					Idp:      respMap["ownerIdp"].(string),
					OpaqueId: respMap["ownerOpaqueId"].(string),
					Type:     user.UserType_USER_TYPE_PRIMARY,
				},
			},
			Root: &provider.ResourceId{
				StorageId: respMap["rootStorageId"].(string),
				OpaqueId:  respMap["rootOpaqueId"].(string),
			},
			Name: respMap["name"].(string),
			Quota: &provider.Quota{
				QuotaMaxBytes: uint64(respMap["quotaMaxBytes"].(float64)),
				QuotaMaxFiles: uint64(respMap["quotaMaxFiles"].(float64)),
			},
			SpaceType: respMap["spaceType"].(string),
			Mtime: &types.Timestamp{
				Seconds: uint64(respMap["mTimeSeconds"].(float64)),
			},
		}
	}
	return spaces, err
}
