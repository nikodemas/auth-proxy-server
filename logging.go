package main

// logging module provides various logging methods
//
// Copyright (c) 2020 - Valentin Kuznetsov <vkuznet@gmail.com>
//

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
)

// helper function to produce UTC time prefixed output
func utcMsg(data []byte) string {
	var msg string
	if Config.UTC {
		msg = fmt.Sprintf("[" + time.Now().UTC().String() + "] " + string(data))
	} else {
		msg = fmt.Sprintf("[" + time.Now().String() + "] " + string(data))
		//     msg = fmt.Sprintf("[" + time.Now().UTC().Format("2006-01-02T15:04:05.999Z") + " UTC] " + string(data))
	}
	return msg
}

// custom rotate logger
type rotateLogWriter struct {
	RotateLogs *rotatelogs.RotateLogs
}

func (w rotateLogWriter) Write(data []byte) (int, error) {
	return w.RotateLogs.Write([]byte(utcMsg(data)))
}

// custom logger
type logWriter struct {
}

func (writer logWriter) Write(data []byte) (int, error) {
	return fmt.Print(utcMsg(data))
}

// helper function to log every single user request
func logRequest(w http.ResponseWriter, r *http.Request, start time.Time, cauth string, status int) {
	// our apache configuration
	// CustomLog "||@APACHE2_ROOT@/bin/rotatelogs -f @LOGDIR@/access_log_%Y%m%d.txt 86400" \
	//   "%t %v [client: %a] [backend: %h] \"%r\" %>s [data: %I in %O out %b body %D us ] [auth: %{SSL_PROTOCOL}x %{SSL_CIPHER}x \"%{SSL_CLIENT_S_DN}x\" \"%{cms-auth}C\" ] [ref: \"%{Referer}i\" \"%{User-Agent}i\" ]"
	//     status := http.StatusOK
	var aproto, cipher string
	if r != nil && r.TLS != nil {
		if r.TLS.Version == tls.VersionTLS10 {
			aproto = "TLS10"
		} else if r.TLS.Version == tls.VersionTLS11 {
			aproto = "TLS11"
		} else if r.TLS.Version == tls.VersionTLS12 {
			aproto = "TLS12"
		} else if r.TLS.Version == tls.VersionTLS13 {
			aproto = "TLS13"
		} else if r.TLS.Version == tls.VersionSSL30 {
			aproto = "SSL30"
		} else {
			aproto = fmt.Sprintf("TLS version: %+v", r.TLS.Version)
		}
		cipher = tls.CipherSuiteName(r.TLS.CipherSuite)
	} else {
		aproto = fmt.Sprintf("No TLS")
		cipher = "None"
	}
	if cauth == "" {
		cauth = fmt.Sprintf("%v", r.Header.Get("Cms-Authn-Method"))
	}
	authMsg := fmt.Sprintf("[auth: %v %v \"%v\" %v]", aproto, cipher, r.Header.Get("Cms-Auth-Cert"), cauth)
	respHeader := w.Header()
	dataMsg := fmt.Sprintf("[data: %v in %v out]", r.ContentLength, respHeader.Get("Content-Length"))
	referer := r.Referer()
	if referer == "" {
		referer = "-"
	}
	addr := fmt.Sprintf("[client: %v] [backend: %v]", r.Header.Get("X-Forwarded-Host"), r.RemoteAddr)
	refMsg := fmt.Sprintf("[ref: \"%s\" \"%v\"]", referer, r.Header.Get("User-Agent"))
	respMsg := fmt.Sprintf("[req: %v resp: %v]", time.Since(start), respHeader.Get("Response-Time"))
	log.Printf("%s %s %s %s %d %s %s %s %s\n", addr, r.Method, r.RequestURI, r.Proto, status, dataMsg, authMsg, refMsg, respMsg)
	if Config.StompConfig.Endpoint != "" {
		rTime, _ := strconv.ParseFloat(respHeader.Get("Response-Time-Seconds"), 10)
		rec := LogRecord{
			Method:         r.Method,
			Uri:            r.RequestURI,
			Proto:          r.Proto,
			Status:         int64(status),
			ContentLength:  r.ContentLength,
			AuthProto:      aproto,
			Cipher:         cipher,
			CmsAuthCert:    r.Header.Get("Cms-Auth-Cert"),
			CmsAuth:        cauth,
			Referer:        referer,
			UserAgent:      r.Header.Get("User-Agent"),
			XForwardedHost: r.Header.Get("X-Forwarded-Host"),
			RemoteAddr:     r.RemoteAddr,
			ResponseStatus: respHeader.Get("Response-Status"),
			ResponseTime:   rTime,
			RequestTime:    time.Since(start).Seconds(),
		}
		var data []byte
		var err error
		if Config.LogsHTTPEndpoint != "" {
			hostname, err := os.Hostname()
			if err != nil {
				log.Println("Unable to get hostname", err)
			}
			ltype := Config.LogsHTTPType
			if ltype == "" {
				ltype = "cms"
			}
			producer := Config.LogsHTTPProducer
			if producer == "" {
				producer = "auth"
			}
			prefix := Config.LogsHTTPTypePrefix
			if prefix == "" {
				prefix = "raw"
			}
			r := HTTPRecord{
				Producer:   producer,
				Type:       ltype,
				TypePrefix: prefix,
				Timestamp:  time.Now().Unix(),
				Host:       hostname,
				Data:       rec,
			}
			data, err = json.Marshal(r)
			if err == nil {
				go send(data)
			} else {
				log.Printf("unable to marshal the data, error %v\n", err)
			}
		} else if Config.StompConfig.URI != "" {
			data, err = json.Marshal(rec)
			if err == nil {
				go stompMgr.Send(data)
			} else {
				log.Printf("unable to marshal the data, error %v\n", err)
			}
		}
	}
}

// helper function to send our logs to http logs end-point
func send(data []byte) {
	rurl := Config.LogsHTTPEndpoint
	ctype := "application/json"
	resp, err := http.Post(rurl, ctype, bytes.NewBuffer(data))
	if err != nil {
		log.Printf("unable to send data to %s, error %v\n", rurl, err)
	}
	if Config.Verbose > 0 {
		log.Println(rurl, resp.Proto, resp.Status)
	}
}
