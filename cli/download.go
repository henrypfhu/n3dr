package cli

import (
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/asaskevich/govalidator"
	log "github.com/sirupsen/logrus"
	"github.com/svenfuchs/jq"
	"github.com/thedevsaddam/gojsonq"
)

const (
	pingURI     = "/service/metrics/ping"
	assetURI    = "/service/rest/v1/assets?repository="
	tokenErrMsg = "Token should be either a hexadecimal or \"null\" and not: "
)

// Nexus3 contains the attributes that are used by several functions
type Nexus3 struct {
	URL        string
	User       string
	Pass       string
	Repository string
}

func (n Nexus3) downloadURL(token string) ([]byte, error) {
	assetURL := n.URL + assetURI + n.Repository
	constructDownloadURL := assetURL
	if !(token == "null") {
		constructDownloadURL = assetURL + "&continuationToken=" + token
	}
	u, err := url.Parse(constructDownloadURL)
	if err != nil {
		return nil, err
	}
	log.Info("DownloadURL: ", u)
	urlString := u.String()
	req, err := http.NewRequest("GET", urlString, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(n.User, n.Pass)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Info(resp.StatusCode)
		return nil, errors.New("HTTP response not 200. Does the URL: " + urlString + " exist?")
	}

	bodyBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return bodyBytes, nil
}

func (n Nexus3) continuationToken(token string) (string, error) {
	// The continuationToken should consists of 32 characters and should be a hexadecimal or "null"
	if !((govalidator.IsHexadecimal(token) && govalidator.StringLength(token, "32", "32")) || token == "null") {
		return "", errors.New(tokenErrMsg + token)
	}

	bodyBytes, err := n.downloadURL(token)
	if err != nil {
		return "", err
	}

	op, err := jq.Parse(".continuationToken")
	if err != nil {
		return "", err
	}

	value, err := op.Apply(bodyBytes)
	if err != nil {
		return "", err
	}
	var tokenWithoutQuotes string
	tokenWithoutQuotes = strings.Trim(string(value), "\"")

	return tokenWithoutQuotes, nil
}

func (n Nexus3) continuationTokenRecursion(t string) ([]string, error) {
	token, err := n.continuationToken(t)
	if err != nil {
		return nil, err
	}
	if token == "null" {
		return []string{token}, nil
	}
	tokenSlice, err := n.continuationTokenRecursion(token)
	if err != nil {
		return nil, err
	}
	return append(tokenSlice, token), nil
}

func createArtifact(d string, f string, content string) error {
	err := os.MkdirAll(d, os.ModePerm)
	if err != nil {
		return err
	}

	file, err := os.Create(filepath.Join(d, f))
	if err != nil {
		return err
	}

	file.WriteString(content)
	defer file.Close()
	return nil
}

func (n Nexus3) artifactName(url string) (string, string, error) {
	log.Info("Validate whether: '" + url + "' is an URL")
	if !govalidator.IsURL(url) {
		return "", "", errors.New(url + " is not an URL")
	}

	re := regexp.MustCompile("^.*/" + n.Repository + "/(.*)/(.+)$")
	match := re.FindStringSubmatch(url)
	if match == nil {
		return "", "", errors.New("URL: '" + url + "' does not seem to contain an artifactName")
	}

	d := match[1]
	log.Info("ArtifactName directory: " + d)

	f := match[2]
	log.Info("ArtifactName file: " + f)

	return d, f, nil
}

func (n Nexus3) downloadArtifact(url string) error {
	d, f, err := n.artifactName(url)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(n.User, n.Pass)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	createArtifact(filepath.Join("download", n.Repository, d), f, string(body))
	return nil
}

func (n Nexus3) downloadURLs() ([]interface{}, error) {
	var downloadURLsInterfaceArrayAll []interface{}
	continuationTokenMap, err := n.continuationTokenRecursion("null")
	if err != nil {
		return nil, err
	}

	for tokenNumber, token := range continuationTokenMap {
		tokenNumberString := strconv.Itoa(tokenNumber)
		log.Info("ContinuationToken: " + token + "; ContinuationTokenNumber: " + tokenNumberString)
		bytes, err := n.downloadURL(token)
		if err != nil {
			return nil, err
		}
		json := string(bytes)

		jq := gojsonq.New().JSONString(json)
		downloadURLsInterface := jq.From("items").Pluck("downloadUrl")

		downloadURLsInterfaceArray := downloadURLsInterface.([]interface{})
		downloadURLsInterfaceArrayAll = append(downloadURLsInterfaceArrayAll, downloadURLsInterfaceArray...)
	}

	return downloadURLsInterfaceArrayAll, nil
}

// StoreArtifactsOnDisk downloads all artifacts from nexus and saves them on disk
func (n Nexus3) StoreArtifactsOnDisk() error {
	log.SetReportCaller(true)
	urls, err := n.downloadURLs()
	if err != nil {
		return err
	}
	for _, downloadURL := range urls {
		n.downloadArtifact(fmt.Sprint(downloadURL))
	}
	return nil
}