package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"io"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/go-github/v42/github"
	"golang.org/x/oauth2"
)

const EventDateFormat = "2006-01-02"

var (
	githubToken   = os.Getenv("GITHUB_TOKEN")
	githubOrg     = os.Getenv("GITHUB_ORG")
	bucketName    = os.Getenv("BUCKET_NAME")
	bucketPrefix  = os.Getenv("FOLDER_PREFIX")
	bookmarkTable = os.Getenv("BOOKMARK_TABLE")
)

var (
	dynamodbSvc  *dynamodb.Client
	s3Uploader   *manager.Uploader
	githubClient *github.Client

	bm *bookmark
)

type bookmark struct {
	EventDate, CreatedAt string
	From, To             time.Time
}

func init() {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Printf("error: %v", err)
		return
	}
	timeZone := os.Getenv("TIME_ZONE")

	if timeZone != "" {
		loc, err := time.LoadLocation(timeZone)
		if err != nil {
			log.Fatalf("could not set timezone: %s", timeZone)
		}
		time.Local = loc
	}

	s3Svc := s3.NewFromConfig(cfg)
	s3Uploader = manager.NewUploader(s3Svc)
	dynamodbSvc = dynamodb.NewFromConfig(cfg)

	ctx := context.Background()
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: githubToken})
	tokenClient := oauth2.NewClient(ctx, tokenSource)
	githubClient = github.NewClient(tokenClient)

	bm, err = getBookmark()
	if err != nil {
		log.Println(err.Error())
		now := time.Now()
		bm = newBookmark(now, now.Add(1*time.Hour))
	}
}

func main() {
	lambda.Start(Handler)
}

func Handler() {
	if githubToken == "" || githubOrg == "" || bucketName == "" || bookmarkTable == "" {
		log.Fatal("You need to specify a non-empty value for the environment variables GITHUB_TOKEN, GITHUB_ORG, BUCKET_NAME, BOOKMARK_TABLE")
	}

	if bucketPrefix == "" {
		bucketPrefix = "Github/Audit"
	}

	auditEntries := getAuditEntries()
	log.Printf("%d audit entries fetched\n", len(auditEntries))
	compressAndUploadToS3(auditEntries)
	newBookmark := newBookmark(bm.To, bm.To.Add(1*time.Hour))
	err := addBookmark(newBookmark)
	if err != nil {
		log.Printf("could not insert next bookmark: %v\n", err)
	}
}

func newBookmark(from, to time.Time) *bookmark {
	return &bookmark{
		EventDate: time.Now().Format(EventDateFormat),
		CreatedAt: time.Now().Format(time.RFC3339),
		From:      from,
		To:        to,
	}
}

func getAuditEntries() []*github.AuditEntry {
	getOpts := getAuditLogOptions()
	auditEntries, response := doGetAuditEntries(getOpts)
	for response.After != "" {
		getOpts.ListCursorOptions.After = response.After
		var ae []*github.AuditEntry
		ae, response = doGetAuditEntries(getOpts)
		auditEntries = append(auditEntries, ae...)
	}
	return auditEntries
}

func doGetAuditEntries(getOpts *github.GetAuditLogOptions) ([]*github.AuditEntry, *github.Response) {
	ctx := context.Background()
	logs, response, err := githubClient.Organizations.GetAuditLog(ctx, githubOrg, getOpts)
	if err != nil {
		log.Fatalf("unable to fetch audit entries: %s\n", err)
	}
	return logs, response
}

func getAuditLogOptions() *github.GetAuditLogOptions {
	optionInclude := os.Getenv("AUDIT_LOG_OPTION_INCLUDE")
	if optionInclude == "" {
		optionInclude = "web"
	}
	optionOrder := os.Getenv("AUDIT_LOG_OPTION_ORDER")
	if optionOrder == "" {
		optionOrder = "desc"
	}
	perPage := 30
	optionPerPage := os.Getenv("AUDIT_LOG_OPTION_PER_PAGE")
	if optionPerPage != "" {
		value, err := strconv.Atoi(optionPerPage)
		if err != nil {
			log.Printf("could not convert %s to int, using default value for per page option\n", optionPerPage)
		} else {
			perPage = value
		}
	}
	optionPhrase := getSearchPhrase()
	log.Printf("phrase: %s, include: %s, order: %s, perPage: %v", optionPhrase, optionInclude, optionOrder, perPage)
	return &github.GetAuditLogOptions{
		Include: github.String(optionInclude),
		Phrase:  github.String(optionPhrase),
		Order:   github.String(optionOrder),
		ListCursorOptions: github.ListCursorOptions{
			PerPage: perPage,
		},
	}
}

func getSearchPhrase() string {
	phrase := fmt.Sprintf("created:%s..%s", bm.From, bm.To)
	optionPhrase := os.Getenv("AUDIT_LOG_OPTION_PRHASE")
	if optionPhrase != "" {
		phrase = phrase + " " + optionPhrase
	}
	return phrase
}

func compressAndUploadToS3(logs []*github.AuditEntry) {
	if len(logs) < 1 {
		log.Println("nothing to upload")
		return
	}
	var buffer bytes.Buffer
	for _, record := range logs {
		body, err := json.Marshal(record)
		if err != nil {
			log.Fatalf("Error occured during marshaling. Error: %v", err)
		}
		buffer.Write(body)
		buffer.WriteString("\n")
	}

	now := time.Now()
	key := fmt.Sprintf("%s/%d/%d/%d/%d/%s.json.gz", bucketPrefix, now.Year(), now.Month(), now.Day(), now.Hour(), now.Format(time.RFC3339))
	result, err := s3Uploader.Upload(
		context.TODO(),
		&s3.PutObjectInput{
			Bucket: aws.String(bucketName),
			Key:    aws.String(key),
			Body:   compress(buffer.Bytes()),
		})
	if err != nil {
		log.Fatalf("failed to upload. Error: %v", err)
	}
	log.Println("successfully uploaded to", result.Location)
}

func compress(s []byte) io.Reader {
	buffer := bytes.Buffer{}
	zipped := gzip.NewWriter(&buffer)
	zipped.Write(s)
	zipped.Close()
	return bytes.NewReader(buffer.Bytes())
}

func getBookmark() (*bookmark, error) {
	formattedEventDate := time.Now().Format(EventDateFormat)
	queryInput := &dynamodb.QueryInput{
		TableName:              aws.String(bookmarkTable),
		ScanIndexForward:       aws.Bool(false),
		Limit:                  aws.Int32(1),
		KeyConditionExpression: aws.String("EventDate = :EventDate"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":EventDate": &types.AttributeValueMemberS{Value: formattedEventDate},
		},
	}
	data, err := dynamodbSvc.Query(context.TODO(), queryInput)
	if err != nil {
		return nil, err
	}
	if len(data.Items) < 1 {
		return nil, fmt.Errorf("no bookmark found for given date: %s", formattedEventDate)
	}
	book := &bookmark{}
	err = attributevalue.UnmarshalMap(data.Items[0], &book)
	if err != nil {
		return nil, fmt.Errorf("error on unmarshaling: %v\n", err)
	}
	return book, nil
}

func addBookmark(bookmark *bookmark) error {
	data, err := attributevalue.MarshalMap(bookmark)
	if err != nil {
		return err
	}
	_, err = dynamodbSvc.PutItem(context.TODO(), &dynamodb.PutItemInput{
		TableName: aws.String(bookmarkTable),
		Item:      data,
	})
	return err
}
