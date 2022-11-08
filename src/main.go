package main

import (
    "github.com/aws/aws-lambda-go/lambda"
    "github.com/aws/aws-lambda-go/events"
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "net/http"
    "os"
    "strconv"
    "strings"
    "time"
)

const DefaultSlackTimeout = 5 * time.Second

type SlackClient struct {
    WebHookUrl string
    Channel    string
    UserName   string
    TimeOut    time.Duration
}

func getEnv(key, fallback string) string {
    if value, ok := os.LookupEnv(key); ok {
        return value
    }
    return fallback
}

var slack = SlackClient{
    WebHookUrl: getEnv("SLACK_WEBHOOK_URL", "*fallback*"),
    Channel:    getEnv("SLACK_CHANNEL", "*fallback*"),
    UserName:   "aws",
}

type SlackNotification struct {
    Colour          string
    Message         string
    Environment     string
    DeploymentId    string
    Status          string
}

type SlackField struct {
    Title string `json:"title"`
    Value string `json:"value"`
    Short bool   `json:"short,omitempty"`
}

type SlackAttachment struct {
    Colour          string          `json:"color,omitempty"`
    Fallback        string          `json:"fallback,omitempty"`
    CallbackID      string          `json:"callback_id,omitempty"`
    ID              int             `json:"id,omitempty"`
    AuthorID        string          `json:"author_id,omitempty"`
    AuthorName      string          `json:"author_name,omitempty"`
    AuthorSubname   string          `json:"author_subname,omitempty"`
    AuthorLink      string          `json:"author_link,omitempty"`
    AuthorIcon      string          `json:"author_icon,omitempty"`
    Title           string          `json:"title,omitempty"`
    TitleLink       string          `json:"title_link,omitempty"`
    Pretext         string          `json:"pretext,omitempty"`
    Text            string          `json:"text,omitempty"`
    ImageURL        string          `json:"image_url,omitempty"`
    ThumbURL        string          `json:"thumb_url,omitempty"`
    MarkdownIn      []string        `json:"mrkdwn_in,omitempty"`
    Fields          []*SlackField   `json:"fields,omitempty"`
    Timestamp       json.Number     `json:"ts,omitempty"`
}

func (attachment *SlackAttachment) AddField(field SlackField) *SlackAttachment {
	attachment.Fields = append(attachment.Fields, &field)
	return attachment
}

type SlackMessage struct {
    Username    string              `json:"username,omitempty"`
    IconEmoji   string              `json:"icon_emoji,omitempty"`
    Channel     string              `json:"channel,omitempty"`
    Text        string              `json:"text,omitempty"`
    Attachments []*SlackAttachment  `json:"attachments,omitempty"`
}

func (message *SlackMessage) AddAttachment(attachment SlackAttachment) *SlackMessage {
	message.Attachments = append(message.Attachments, &attachment)
	return message
}

func (slack SlackClient) sendHttpRequest(message SlackMessage) error {
    slackBody, _ := json.Marshal(message)
    request, err := http.NewRequest(http.MethodPost, slack.WebHookUrl, bytes.NewBuffer(slackBody))
    if err != nil {
        return err
    }
    request.Header.Add("Content-Type", "application/json")
    if slack.TimeOut == 0 {
        slack.TimeOut = DefaultSlackTimeout
    }
    client := &http.Client{Timeout: slack.TimeOut}
    response, err := client.Do(request)
    if err != nil {
        return err
    }

    buffer := new(bytes.Buffer)
    _, err = buffer.ReadFrom(response.Body)
    if err != nil {
        return err
    }
    if buffer.String() != "ok" {
        return errors.New("Slack Message failed to send")
    }
    return nil
}

func (slack SlackClient) sendNotification(notification SlackNotification) error {
    message := SlackMessage{
        Text:        notification.Message,
        Username:    slack.UserName,
        IconEmoji:   ":rocket",
        Channel:     slack.Channel,
    }

    attachment := SlackAttachment{
        Colour:     notification.Colour,
        Timestamp:  json.Number(strconv.FormatInt(time.Now().Unix(), 10)),
    }

    attachment.AddField(SlackField{
        Title: "Environment",
        Value: notification.Environment,
        Short: true,
    }).AddField(SlackField{
        Title: "Deployment ID",
        Value: notification.DeploymentId,
        Short: true,
    }).AddField(SlackField{
        Title: "Status",
        Value: fmt.Sprintf("*%s*", strings.ToUpper(notification.Status)),
    })

    message.AddAttachment(attachment)

    return slack.sendHttpRequest(message)
}

func formatECSEventDetails(event CodeDeployECSTriggerEvent, sb *strings.Builder) {
    if len(event.LifecycleEvents) > 0 {
        sb.WriteString("*Lifecycle Events*\n")
        for i, lifecycleEvent := range event.LifecycleEvents {
            sb.WriteString(fmt.Sprintf("*Event %d:* %s [*%s*]", i+1, lifecycleEvent.Event, lifecycleEvent.EventStatus))
            if lifecycleEvent.EndTime != "" {
                sb.WriteString(fmt.Sprintf(" %s", lifecycleEvent.EndTime))
            }
            sb.WriteString("\n")
        }
    }

    if event.DeploymentOverview != nil {
        sb.WriteString("*Deployment Overview*\n")
        sb.WriteString(fmt.Sprintf("Succeeded: *%d* | ", event.DeploymentOverview.Succeeded))
        sb.WriteString(fmt.Sprintf("Failed: *%d* | ", event.DeploymentOverview.Failed))
        sb.WriteString(fmt.Sprintf("Skipped: *%d* | ", event.DeploymentOverview.Skipped))
        sb.WriteString(fmt.Sprintf("InProgress: *%d* | ", event.DeploymentOverview.InProgress))
        sb.WriteString(fmt.Sprintf("Pending: *%d*\n\n", event.DeploymentOverview.Pending))
    }

    if event.ErrorInformation != nil {
        sb.WriteString("*Error Information*\n")
        sb.WriteString(fmt.Sprintf("Error Code: *%s*\n", event.ErrorInformation.Code))
        sb.WriteString(fmt.Sprintf("%s\n", event.ErrorInformation.Message))
    }

    if event.RollbackInformation != nil {
        sb.WriteString("*Rollback Information*\n")
        sb.WriteString(fmt.Sprintf("%s\n", event.RollbackInformation.Message))
    }
}

func (slack SlackClient) composeNotification(colour string, icon_emoji string, event CodeDeployECSTriggerEvent) error {
    var sb strings.Builder

    // Slack markdown link format <URL|Anchor Text>
    sb.WriteString("*<")
    sb.WriteString(fmt.Sprintf("https://%s.console.aws.amazon.com/codesuite/codedeploy/deployments/%s", event.Region, event.DeploymentId))
    sb.WriteString("|")
    sb.WriteString(fmt.Sprintf(":mega: AWS CodeDeploy Notification | %s | Account: %s", event.Region, event.AccountId))
    sb.WriteString(">*\n\n")

    sb.WriteString(fmt.Sprintf("%s *ECS Deployment Update*\n\n", icon_emoji))
    formatECSEventDetails(event, &sb)

    message := sb.String()

    notification := SlackNotification{
        Colour:       colour,
        Message:      message,
        Environment:  strings.Title(getEnv("ENVIRONMENT", "Testing")),
        DeploymentId: event.DeploymentId,
        Status:       event.EventStatus,
    }
    return slack.sendNotification(notification)
}

func (slack SlackClient) sendSuccess(event CodeDeployECSTriggerEvent) (err error) {
    return slack.composeNotification("good", ":white_check_mark:", event)
}

func (slack SlackClient) sendNeutral(event CodeDeployECSTriggerEvent) (err error) {
    return slack.composeNotification("grey", ":arrows_counterclockwise:", event)
}

func (slack SlackClient) sendError(event CodeDeployECSTriggerEvent) (err error) {
    return slack.composeNotification("danger", ":x:", event)
}

type ECSTriggerEvent struct {
    Region              string `json:"region"`
    AccountId           string `json:"accountId"`
    EventTriggerName    string `json:"eventTriggerName"`
    ApplicationName     string `json:"applicationName,omitempty"`
    DeploymentId        string `json:"deploymentId"`
    InstanceId          string `json:"instanceId,omitempty"`
    DeploymentGroupName string `json:"deploymentGroupName,omitempty"`
    CreateTime          string `json:"createTime,omitempty"`
    CompleteTime        string `json:"completeTime,omitempty"`
    Status              string `json:"status,omitempty"`
    LastUpdatedAt       string `json:"lastUpdatedAt,omitempty"`
    InstanceStatus      string `json:"instanceStatus,omitempty"`
    LifecycleEvents     string `json:"lifecycleEvents,omitempty"`
    DeploymentOverview  string `json:"deploymentOverview,omitempty"`
    ErrorInformation    string `json:"errorInformation,omitempty"`
    RollbackInformation string `json:"rollbackInformation,omitempty"`
}

type LifecycleEvent struct {
    Event       string `json:"LifecycleEvent"`
    EventStatus string `json:"LifecycleEventStatus"`
    StartTime   string `json:"StartTime,omitempty"`
    EndTime     string `json:"EndTime,omitempty"`
}

type DeploymentOverview struct {
    Succeeded   uint8 `json:"Succeeded"`
    Failed      uint8 `json:"Failed"`
    Skipped     uint8 `json:"Skipped"`
    InProgress  uint8 `json:"InProgress"`
    Pending     uint8 `json:"Pending"`
}

type ErrorInformation struct {
    Code    string `json:"ErrorCode"`
    Message string `json:"ErrorMessage"`
}

type RollbackInformation struct {
    Message                 string `json:"RollbackMessage"`
    TriggeringDeploymentId  string `json:"RollbackTriggeringDeploymentId"`
}

type CodeDeployECSTriggerEvent struct {
    ECSTriggerEvent
    LifecycleEvents     []LifecycleEvent
    DeploymentOverview  *DeploymentOverview
    ErrorInformation    *ErrorInformation
    RollbackInformation *RollbackInformation
    EventStatus         string
}

// https://mariadesouza.com/2017/09/07/custom-unmarshal-json-in-golang/
func (triggerEvent *CodeDeployECSTriggerEvent) UnmarshalJSON(data []byte) error {
    var unmarshalled ECSTriggerEvent
    if err := json.Unmarshal(data, &unmarshalled); err != nil {
        return err
    }

    triggerEvent.Region              = unmarshalled.Region
    triggerEvent.AccountId           = unmarshalled.AccountId
    triggerEvent.EventTriggerName    = unmarshalled.EventTriggerName
    triggerEvent.ApplicationName     = unmarshalled.ApplicationName
    triggerEvent.DeploymentId        = unmarshalled.DeploymentId
    triggerEvent.InstanceId          = unmarshalled.InstanceId
    triggerEvent.DeploymentGroupName = unmarshalled.DeploymentGroupName
    triggerEvent.CreateTime          = unmarshalled.CreateTime
    triggerEvent.CompleteTime        = unmarshalled.CompleteTime
    triggerEvent.Status              = unmarshalled.Status
    triggerEvent.LastUpdatedAt       = unmarshalled.LastUpdatedAt
    triggerEvent.InstanceStatus      = unmarshalled.InstanceStatus

    // Some ECS notifications hold "Status", others "InstanceStatus", so let's condense that into a single field
    if triggerEvent.Status != "" {
        triggerEvent.EventStatus = triggerEvent.Status
    } else if triggerEvent.InstanceStatus != "" {
        triggerEvent.EventStatus = triggerEvent.InstanceStatus
    }

    if unmarshalled.LifecycleEvents != "" {
        var unmarshalledEvents []LifecycleEvent
        if err := json.Unmarshal(json.RawMessage(unmarshalled.LifecycleEvents), &unmarshalledEvents); err != nil {
            return err
        }
        triggerEvent.LifecycleEvents = unmarshalledEvents
    }

    if unmarshalled.DeploymentOverview != "" {
        var unmarshalledDeploymentOverview *DeploymentOverview
        if err := json.Unmarshal(json.RawMessage(unmarshalled.DeploymentOverview), &unmarshalledDeploymentOverview); err != nil {
            return err
        }
        triggerEvent.DeploymentOverview = unmarshalledDeploymentOverview
    }

    if unmarshalled.ErrorInformation != "" {
        var unmarshalledErrorInfo *ErrorInformation
        if err := json.Unmarshal(json.RawMessage(unmarshalled.ErrorInformation), &unmarshalledErrorInfo); err != nil {
            return err
        }
        triggerEvent.ErrorInformation = unmarshalledErrorInfo
    }

    if unmarshalled.RollbackInformation != "" {
        var unmarshalledRollbackInfo *RollbackInformation
        if err := json.Unmarshal(json.RawMessage(unmarshalled.RollbackInformation), &unmarshalledRollbackInfo); err != nil {
            return err
        }

        if unmarshalledRollbackInfo.TriggeringDeploymentId != "" {
            triggerEvent.RollbackInformation = unmarshalledRollbackInfo
        }
    }

    return nil
}

func sendToSlack(event CodeDeployECSTriggerEvent) {
    var err error
    switch strings.Replace(strings.ToUpper(event.EventStatus), "_", "", -1) {
        case "SUCCEEDED":
            err = slack.sendSuccess(event);
        case "CREATED", "INPROGRESS", "READY":
            err = slack.sendNeutral(event);
        default: // Assume error otherwise
            err = slack.sendError(event);
    }

    if err != nil {
       log.Fatal(err)
    }
}

func sendECSDeploymentStatusToSlack(ctx context.Context, snsEvent events.SNSEvent) {
    for _, record := range snsEvent.Records {
		snsRecord := record.SNS

        fmt.Printf("[%s %s] Message = %s \n", record.EventSource, snsRecord.Timestamp, snsRecord.Message)

		var event = CodeDeployECSTriggerEvent{}
		if err := json.Unmarshal(json.RawMessage(snsRecord.Message), &event); err != nil {
		    log.Fatal("JSON decode error!")
            return
        }

		sendToSlack(event)
	}
}

func main() {
	lambda.Start(sendECSDeploymentStatusToSlack)
}
