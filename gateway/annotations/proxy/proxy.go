/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package proxy

import (
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"regexp"
	"strings"

	"github.com/goodrain/rainbond/gateway/annotations/parser"
	"github.com/goodrain/rainbond/gateway/annotations/resolver"
	"github.com/goodrain/rainbond/gateway/controller/config"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http/httpguts"
)

// Config returns the proxy timeout to use in the upstream server/s
type Config struct {
	BodySize            int               `json:"bodySize"`
	ConnectTimeout      int               `json:"connectTimeout"`
	SendTimeout         int               `json:"sendTimeout"`
	ReadTimeout         int               `json:"readTimeout"`
	BuffersNumber       int               `json:"buffersNumber"`
	BufferSize          string            `json:"bufferSize"`
	CookieDomain        string            `json:"cookieDomain"`
	CookiePath          string            `json:"cookiePath"`
	NextUpstream        string            `json:"nextUpstream"`
	NextUpstreamTimeout int               `json:"nextUpstreamTimeout"`
	NextUpstreamTries   int               `json:"nextUpstreamTries"`
	ProxyRedirectFrom   string            `json:"proxyRedirectFrom"`
	ProxyRedirectTo     string            `json:"proxyRedirectTo"`
	RequestBuffering    string            `json:"requestBuffering"`
	ProxyBuffering      string            `json:"proxyBuffering"`
	SetHeaders          map[string]string `json:"setHeaders"`
	ResponseHeaders     map[string]string `json:"responseHeaders"`
}

// Validation validation nginx parameters
func (s *Config) Validation() error {
	defBackend := config.NewDefault()
	for k, v := range s.SetHeaders {
		if !httpguts.ValidHeaderFieldName(k) {
			return fmt.Errorf("header %s name is valid", k)
		}
		if !httpguts.ValidHeaderFieldValue(v) {
			return fmt.Errorf("header %s value %s is valid", k, v)
		}
	}

	for k, v := range s.ResponseHeaders {
		if !httpguts.ValidHeaderFieldName(k) {
			return fmt.Errorf("response header %s name is valid", k)
		}
		if !httpguts.ValidHeaderFieldValue(v) {
			return fmt.Errorf("response header %s value %s is valid", k, v)
		}
	}

	if !s.validateBuffering(s.ProxyBuffering) {
		logrus.Warningf("invalid proxy buffering: %s; use the default one: %s", s.ProxyBuffering, defBackend.ProxyBuffering)
		s.ProxyBuffering = defBackend.ProxyBuffering
	}
	if !s.validateBufferSize() {
		logrus.Warningf("invalid proxy buffer size: %s; use the default one: %s", s.BufferSize, defBackend.ProxyBufferSize)
		s.BufferSize = defBackend.ProxyBufferSize
	}
	if s.BuffersNumber <= 0 {
		logrus.Warningf("invalid buffer number: %d; use the default one: %d", s.BuffersNumber, defBackend.ProxyBuffersNumber)
		s.BuffersNumber = defBackend.ProxyBuffersNumber
	}
	if !s.validateBuffering(s.RequestBuffering) {
		logrus.Warningf("invalid reqeust buffering: %s; use the default one: %s", s.RequestBuffering, defBackend.ProxyRequestBuffering)
		s.RequestBuffering = defBackend.ProxyRequestBuffering
	}
	if s.CookieDomain == "" {
		s.CookieDomain = defBackend.ProxyCookieDomain
	}
	if s.CookiePath == "" {
		s.CookiePath = defBackend.ProxyCookiePath
	}
	return nil
}

func (s *Config) validateBufferSize() bool {
	reg := regexp.MustCompile(`^[1-9]\d*k$`)
	return reg.MatchString(s.BufferSize)
}

func (s *Config) validateBuffering(buffering string) bool {
	return buffering == "off" || buffering == "on"
}

// Equal tests for equality between two Configuration types
func (s *Config) Equal(l2 *Config) bool {
	if s == l2 {
		return true
	}
	if s == nil || l2 == nil {
		return false
	}
	if s.BodySize != l2.BodySize {
		return false
	}
	if s.ConnectTimeout != l2.ConnectTimeout {
		return false
	}
	if s.SendTimeout != l2.SendTimeout {
		return false
	}
	if s.ReadTimeout != l2.ReadTimeout {
		return false
	}
	if s.BuffersNumber != l2.BuffersNumber {
		return false
	}
	if s.BufferSize != l2.BufferSize {
		return false
	}
	if s.CookieDomain != l2.CookieDomain {
		return false
	}
	if s.CookiePath != l2.CookiePath {
		return false
	}
	if s.NextUpstream != l2.NextUpstream {
		return false
	}
	if s.NextUpstreamTries != l2.NextUpstreamTries {
		return false
	}
	if s.RequestBuffering != l2.RequestBuffering {
		return false
	}
	if s.ProxyRedirectFrom != l2.ProxyRedirectFrom {
		return false
	}
	if s.ProxyRedirectTo != l2.ProxyRedirectTo {
		return false
	}
	if s.ProxyBuffering != l2.ProxyBuffering {
		return false
	}

	if len(s.SetHeaders) != len(l2.SetHeaders) {
		return false
	}

	if len(s.ResponseHeaders) != len(l2.ResponseHeaders) {
		return false
	}
	for k, v := range s.SetHeaders {
		if l2.SetHeaders[k] != v {
			return false
		}
	}

	for k, v := range s.ResponseHeaders {
		if l2.ResponseHeaders[k] != v {
			return false
		}
	}
	return true
}

type proxy struct {
	r resolver.Resolver
}

// NewParser creates a new reverse proxy configuration annotation parser
func NewParser(r resolver.Resolver) parser.IngressAnnotation {
	return proxy{r}
}

// ParseAnnotations parses the annotations contained in the ingress
// rule used to configure upstream check parameters
func (a proxy) Parse(meta *metav1.ObjectMeta) (interface{}, error) {
	defBackend := a.r.GetDefaultBackend()
	config := &Config{}

	var err error

	config.ConnectTimeout, err = parser.GetIntAnnotation("proxy-connect-timeout", meta)
	if err != nil {
		config.ConnectTimeout = defBackend.ProxyConnectTimeout
	}

	config.SendTimeout, err = parser.GetIntAnnotation("proxy-send-timeout", meta)
	if err != nil {
		config.SendTimeout = defBackend.ProxySendTimeout
	}

	config.ReadTimeout, err = parser.GetIntAnnotation("proxy-read-timeout", meta)
	if err != nil {
		config.ReadTimeout = defBackend.ProxyReadTimeout
	}

	config.BuffersNumber, err = parser.GetIntAnnotation("proxy-buffers-number", meta)
	if err != nil {
		config.BuffersNumber = defBackend.ProxyBuffersNumber
	}

	config.BufferSize, err = parser.GetStringAnnotation("proxy-buffer-size", meta)
	if err != nil {
		config.BufferSize = defBackend.ProxyBufferSize
	}

	config.CookiePath, err = parser.GetStringAnnotation("proxy-cookie-path", meta)
	if err != nil {
		config.CookiePath = defBackend.ProxyCookiePath
	}

	config.CookieDomain, err = parser.GetStringAnnotation("proxy-cookie-domain", meta)
	if err != nil {
		config.CookieDomain = defBackend.ProxyCookieDomain
	}

	config.BodySize, err = parser.GetIntAnnotation("proxy-body-size", meta)
	if err != nil {
		config.BodySize = defBackend.ProxyBodySize
	}

	config.NextUpstream, err = parser.GetStringAnnotation("proxy-next-upstream", meta)
	if err != nil {
		config.NextUpstream = defBackend.ProxyNextUpstream
	}

	config.NextUpstreamTries, err = parser.GetIntAnnotation("proxy-next-upstream-tries", meta)
	if err != nil {
		config.NextUpstreamTries = defBackend.ProxyNextUpstreamTries
	}

	config.NextUpstreamTimeout, err = parser.GetIntAnnotation("proxy-next-upstream-timeout", meta)
	if err != nil {
		config.NextUpstreamTimeout = defBackend.ProxyNextUpstreamTimeout
	}

	config.RequestBuffering, err = parser.GetStringAnnotation("proxy-request-buffering", meta)
	if err != nil {
		config.RequestBuffering = defBackend.ProxyRequestBuffering
	}

	config.ProxyRedirectFrom, err = parser.GetStringAnnotation("proxy-redirect-from", meta)
	if err != nil {
		config.ProxyRedirectFrom = defBackend.ProxyRedirectFrom
	}

	config.ProxyRedirectTo, err = parser.GetStringAnnotation("proxy-redirect-to", meta)
	if err != nil {
		config.ProxyRedirectTo = defBackend.ProxyRedirectTo
	}

	config.ProxyBuffering, err = parser.GetStringAnnotation("proxy-buffering", meta)
	if err != nil {
		config.ProxyBuffering = defBackend.ProxyBuffering
	}

	config.SetHeaders = make(map[string]string)
	//default header
	for k, v := range defBackend.ProxySetHeaders {
		config.SetHeaders[k] = v
	}

	// request headers
	setHeaders, err := parser.GetStringAnnotationWithPrefix("set-header-", meta)
	if err != nil && !strings.Contains(err.Error(), "ingress rule without annotations") {
		logrus.Debugf("get header annotation failure %s", err.Error())
	}
	for k, v := range setHeaders {
		if v == "empty" {
			v = ""
		}
		config.SetHeaders[k] = v
	}

	// response header
	responseHeaders, err := parser.GetStringAnnotationWithPrefix("resp-header-", meta)
	if err != nil && !strings.Contains(err.Error(), "ingress rule without annotations") {
		logrus.Debugf("get header annotation failure %s", err.Error())
	}
	for k, v := range responseHeaders {
		if v == "empty" {
			v = ""
		}
		config.ResponseHeaders[k] = v
	}
	return config, nil
}
