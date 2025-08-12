package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/textract"
	"github.com/aws/aws-sdk-go-v2/service/textract/types"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

func main() {
	ctx := context.Background()
	gmailService := authenticateGmail(ctx)
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("aws config: %v", err)
	}
	textractClient := textract.NewFromConfig(cfg)

	targetNames := loadTargetNamesFromEnv()
	if len(targetNames) == 0 {
		log.Fatalf("No target names configured. Set TARGET_NAMES or TARGET_NAMES_JSON.")
	}
	denyNames := loadDenyNamesFromEnv()

	senderAddress := getenvDefault("SENDER_ADDRESS", "USPSInformeddelivery@email.informeddelivery.usps.com")
	labelName := getenvDefault("USPS_LABEL", "USPS")
	twoDaysAgo := time.Now().AddDate(0, 0, -2).Format("2006/01/02")

	uspsLabelId, err := findOrCreateLabel(gmailService, "me", labelName)
	if err != nil {
		log.Fatalf("Error finding or creating %s label: %v", labelName, err)
	}

	query := fmt.Sprintf("from:%s after:%s -label:%s", senderAddress, twoDaysAgo, labelName)
	log.Printf("Searching for emails with query: %s", query)
	messages, err := listMessages(gmailService, "me", query)
	if err != nil {
		log.Fatalf("Unable to retrieve messages: %v", err)
	}
	log.Printf("Found %d messages", len(messages))

	for _, message := range messages {
		msg, err := gmailService.Users.Messages.Get("me", message.Id).Do()
		if err != nil {
			log.Printf("Error retrieving message %s: %v", message.Id, err)
			continue
		}
		log.Printf("Processing: %s", getSubject(msg))

		images := extractImages(gmailService, msg)
		nameFound := false
		foundName := ""

		for i, imageData := range images {
			text, err := detectTextWithTextract(ctx, textractClient, imageData)
			if err != nil {
				log.Printf("Error detecting text: %v", err)
				continue
			}
			lowerText := strings.ToLower(text)
			if containsAny(lowerText, denyNames) {
				log.Printf("Image %d contains deny term; skipping", i+1)
				continue
			}
			for _, name := range targetNames {
				if strings.Contains(lowerText, strings.ToLower(name)) {
					log.Printf("Found '%s' in image %d!", name, i+1)
					nameFound = true
					foundName = name
					break
				}
			}
			if nameFound {
				break
			}
		}

		if nameFound {
			log.Printf("Adding %s label and removing from inbox (found name: %s)", labelName, foundName)
			err = modifyMessage(gmailService, "me", message.Id, []string{uspsLabelId}, []string{"INBOX"})
		} else {
			log.Printf("Trashing email (no target names found)")
			err = trashMessage(gmailService, "me", message.Id)
		}
		if err != nil {
			log.Printf("Error processing: %v", err)
		}
	}
	log.Println("Complete")
}

// Helper functions
func loadTargetNamesFromEnv() []string {
	if js := os.Getenv("TARGET_NAMES_JSON"); js != "" {
		var arr []string
		if err := json.Unmarshal([]byte(js), &arr); err == nil {
			return dedupeNonEmpty(arr)
		}
	}
	if csv := os.Getenv("TARGET_NAMES"); csv != "" {
		parts := strings.Split(csv, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return dedupeNonEmpty(parts)
	}
	return nil
}

func loadDenyNamesFromEnv() []string {
	if js := os.Getenv("DENY_NAMES_JSON"); js != "" {
		var arr []string
		if err := json.Unmarshal([]byte(js), &arr); err == nil {
			return dedupeNonEmpty(arr)
		}
	}
	if csv := os.Getenv("DENY_NAMES"); csv != "" {
		parts := strings.Split(csv, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		return dedupeNonEmpty(parts)
	}
	return nil
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(haystack, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	return out
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func findOrCreateLabel(service *gmail.Service, userId, labelName string) (string, error) {
	labels, err := service.Users.Labels.List(userId).Do()
	if err != nil {
		return "", err
	}
	for _, label := range labels.Labels {
		if label.Name == labelName {
			return label.Id, nil
		}
	}
	newLabel, err := service.Users.Labels.Create(userId, &gmail.Label{
		Name:                  labelName,
		LabelListVisibility:   "labelShow",
		MessageListVisibility: "show",
	}).Do()
	if err != nil {
		return "", err
	}
	return newLabel.Id, nil
}

func authenticateGmail(ctx context.Context) *gmail.Service {
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}
	oauthCfg, err := google.ConfigFromJSON(b, gmail.GmailModifyScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}
	client := getClient(oauthCfg)
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Fatalf("Unable to create Gmail service: %v", err)
	}
	return srv
}

func getClient(oauthCfg *oauth2.Config) *http.Client {
	tokFile := "token.json"
	tok, err := tokenFromFile(tokFile)
	if err != nil {
		tok = getTokenFromWeb(oauthCfg)
		if err := saveToken(tokFile, tok); err != nil {
			log.Printf("warning: failed to save token: %v", err)
		}
	}
	return oauthCfg.Client(context.Background(), tok)
}

func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			log.Printf("warning: closing token file: %v", cerr)
		}
	}()
	tok := &oauth2.Token{}
	if err := json.NewDecoder(f).Decode(tok); err != nil {
		return nil, err
	}
	return tok, nil
}

func getTokenFromWeb(oauthCfg *oauth2.Config) *oauth2.Token {
	authURL := oauthCfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser then type the authorization code:\n%v\n", authURL)
	var authCode string
	if _, err := fmt.Scan(&authCode); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}
	tok, err := oauthCfg.Exchange(context.TODO(), authCode)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return tok
}

func saveToken(path string, token *oauth2.Token) error {
	fmt.Printf("Saving credential file to: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			log.Printf("warning: closing token file: %v", cerr)
		}
	}()
	if err := json.NewEncoder(f).Encode(token); err != nil {
		return err
	}
	return nil
}

func listMessages(service *gmail.Service, userId string, query string) ([]*gmail.Message, error) {
	var messages []*gmail.Message
	pageToken := ""
	for {
		req := service.Users.Messages.List(userId).Q(query)
		if pageToken != "" {
			req.PageToken(pageToken)
		}
		res, err := req.Do()
		if err != nil {
			return nil, err
		}
		messages = append(messages, res.Messages...)
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}
	return messages, nil
}

func getSubject(msg *gmail.Message) string {
	for _, header := range msg.Payload.Headers {
		if header.Name == "Subject" {
			return header.Value
		}
	}
	return "No Subject"
}

func extractImages(service *gmail.Service, msg *gmail.Message) [][]byte {
	var images [][]byte
	var processPart func(*gmail.MessagePart)
	processPart = func(part *gmail.MessagePart) {
		if strings.HasPrefix(part.MimeType, "image/") && part.Body != nil && part.Body.AttachmentId != "" {
			attachment, err := service.Users.Messages.Attachments.Get("me", msg.Id, part.Body.AttachmentId).Do()
			if err != nil {
				log.Printf("Error getting attachment: %v", err)
				return
			}
			data, err := base64.URLEncoding.DecodeString(attachment.Data)
			if err != nil {
				log.Printf("Error decoding attachment: %v", err)
				return
			}
			images = append(images, data)
		}
		for _, childPart := range part.Parts {
			processPart(childPart)
		}
	}
	if msg.Payload != nil {
		processPart(msg.Payload)
	}
	return images
}

func detectTextWithTextract(ctx context.Context, client *textract.Client, imageBytes []byte) (string, error) {
	out, err := client.DetectDocumentText(ctx, &textract.DetectDocumentTextInput{
		Document: &types.Document{Bytes: imageBytes},
	})
	if err != nil {
		return "", err
	}
	var fullText strings.Builder
	for _, block := range out.Blocks {
		if block.BlockType == types.BlockTypeLine && block.Text != nil {
			if _, err := fullText.WriteString(*block.Text + " "); err != nil {
				// strings.Builder should not error, but handle to satisfy linters
				return "", err
			}
		}
	}
	return fullText.String(), nil
}

func modifyMessage(service *gmail.Service, userId, messageId string, addLabels, removeLabels []string) error {
	modification := &gmail.ModifyMessageRequest{AddLabelIds: addLabels, RemoveLabelIds: removeLabels}
	_, err := service.Users.Messages.Modify(userId, messageId, modification).Do()
	return err
}

func trashMessage(service *gmail.Service, userId, messageId string) error {
	_, err := service.Users.Messages.Trash(userId, messageId).Do()
	return err
}
