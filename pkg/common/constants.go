// MIT License
//
// Copyright (c) 2024 PlexObject Solutions, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Package common provides shared version information for k8-highlander.
//
// This file contains version information variables that are injected during
// the build process. These variables provide runtime access to version and
// build details, which are useful for logging, monitoring, and debugging.
//
// The version information is typically set during build time using ldflags, e.g.:
//   go build -ldflags "-X k8-highlander/pkg/common.VERSION=v1.0.0 -X k8-highlander/pkg/common.BuildInfo=2024-03-23"
//
// This enables deployment tracking, monitoring, and proper versioning of the
// application in production environments.

package common

// VERSION is the version of the application
var VERSION string

// BuildInfo contains build information
var BuildInfo string

const PACKAGE = "k8-highlander"
