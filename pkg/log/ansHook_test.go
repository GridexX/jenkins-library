package log

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/SAP/jenkins-library/pkg/ans"
	"github.com/SAP/jenkins-library/pkg/xsuaa"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestANSHook_Levels(t *testing.T) {
	// hook, _ := registerANSHookIfConfigured(defaultConfiguration(), "", &ansMock{})
	// assert.Equal(t, []logrus.Level{logrus.WarnLevel, logrus.ErrorLevel, logrus.PanicLevel, logrus.FatalLevel},
	// 	hook.Levels())
}

func TestANSHook_setupEventTemplate(t *testing.T) {
	t.Run("good", func(t *testing.T) {
		t.Run("setup event without template", func(t *testing.T) {
			event, _ := setupEventTemplate(createConfiguration(), defaultCorrelationID())
			assert.Equal(t, defaultEvent(), event, "unexpected event data")
		})
		t.Run("setup event from default template", func(t *testing.T) {
			event, _ := setupEventTemplate(createConfiguration(customerEventString()), defaultCorrelationID())
			assert.Equal(t, defaultEvent(), event, "unexpected event data")
		})
		t.Run("setup event with category", func(t *testing.T) {
			event, _ := setupEventTemplate(createConfiguration(customerEventString(map[string]interface{}{"Category": "ALERT"})), defaultCorrelationID())
			assert.Equal(t, "", event.Category, "unexpected category data")
		})
		t.Run("setup event with severity", func(t *testing.T) {
			event, _ := setupEventTemplate(createConfiguration(customerEventString(map[string]interface{}{"Severity": "WARNING"})), defaultCorrelationID())
			assert.Equal(t, "", event.Severity, "unexpected severity  data")
		})
		t.Run("setup event with invalid category", func(t *testing.T) {
			event, _ := setupEventTemplate(createConfiguration(customerEventString(map[string]interface{}{"Category": "invalid"})), defaultCorrelationID())
			assert.Equal(t, "", event.Category, "unexpected event data")
		})
		t.Run("setup event with priority", func(t *testing.T) {
			event, _ := setupEventTemplate(createConfiguration(customerEventString(map[string]interface{}{"Priority": "1"})), defaultCorrelationID())
			assert.Equal(t, 1, event.Priority, "unexpected event data")
		})
		t.Run("setup event with omitted priority 0", func(t *testing.T) {
			event, err := setupEventTemplate(createConfiguration(customerEventString(map[string]interface{}{"Priority": "0"})), defaultCorrelationID())
			assert.Equal(t, nil, err, "priority 0 must not fail")
			assert.Equal(t, 0, event.Priority, "unexpected priority data ")
		})
	})

	t.Run("bad", func(t *testing.T) {
		t.Run("setup event with invalid priority", func(t *testing.T) {
			_, err := setupEventTemplate(createConfiguration(customerEventString(map[string]interface{}{"Priority": "-1"})), defaultCorrelationID())
			assert.Contains(t, err.Error(), "Priority must be 1 or greater", "unexpected error text")
		})
		t.Run("setup event with invalid variable name", func(t *testing.T) {
			_, err := setupEventTemplate(createConfiguration(customerEventString(map[string]interface{}{"Invalid": "invalid"})), defaultCorrelationID())
			assert.Contains(t, err.Error(), "could not be unmarshalled", "unexpected error text")
		})
	})
}

func TestANSHook_newANSHook(t *testing.T) {
	t.Parallel()
	type args struct {
		serviceKey    string
		eventTemplate string
	}
	tests := []struct {
		name                     string
		args                     args
		eventTemplateFileContent string
		checkErr                 error
		wantEvent                ans.Event
		wantErrMsg               string
	}{
		{
			name:      "Straight forward test",
			args:      args{serviceKey: defaultServiceKeyJSON},
			wantEvent: defaultEvent(),
		},
		{
			name:       "No service key yields error",
			wantErrMsg: "cannot initialize SAP Alert Notification Service due to faulty serviceKey json: error unmarshalling ANS serviceKey: unexpected end of JSON input",
		},
		{
			name:       "Fails on check error",
			args:       args{serviceKey: defaultServiceKeyJSON},
			wantErrMsg: "check http request to SAP Alert Notification Service failed; not setting up the ANS hook: check failed",
			checkErr:   fmt.Errorf("check failed"),
		},
		{
			name:                     "With event template as file",
			args:                     args{serviceKey: defaultServiceKeyJSON},
			eventTemplateFileContent: `{"priority":123}`,
			wantEvent:                mergeEvents(t, defaultEvent(), ans.Event{Priority: 123}),
		},
		{
			name:      "With event template as string",
			args:      args{serviceKey: defaultServiceKeyJSON, eventTemplate: `{"priority":123}`},
			wantEvent: mergeEvents(t, defaultEvent(), ans.Event{Priority: 123}),
		},
		{
			name:                     "With event template from two sources, string overwrites file",
			args:                     args{serviceKey: defaultServiceKeyJSON, eventTemplate: `{"priority":789}`},
			eventTemplateFileContent: `{"priority":123}`,
			wantEvent:                mergeEvents(t, defaultEvent(), ans.Event{Priority: 789}),
		},
		{
			name:       "Fails on validation error",
			args:       args{serviceKey: defaultServiceKeyJSON, eventTemplate: `{"priority":-1}`},
			wantErrMsg: "did not initialize SAP Alert Notification Service due to faulty event template json: Priority must be 1 or greater: event JSON failed the validation",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var testEventTemplateFilePath string
			if len(tt.eventTemplateFileContent) > 0 {
				testEventTemplateFilePath = writeTempFile(t, tt.eventTemplateFileContent)
				defer os.Remove(testEventTemplateFilePath)
			}

			ansConfig := ans.Configuration{
				EventTemplate: tt.args.eventTemplate,
			}
			clientMock := ansMock{checkErr: tt.checkErr}
			if err := registerANSHookIfConfigured(ansConfig, testCorrelationID, &clientMock); err != nil {
				assert.EqualError(t, err, tt.wantErrMsg, "Error mismatch")
			} else {
				assert.Equal(t, tt.wantErrMsg, "", "There was an error expected")
				assert.Equal(t, defaultANSClient(), clientMock.a, "new ANSHook not as expected")
				//				assert.Equal(t, tt.wantEvent, got.eventTemplate, "new ANSHook not as expected")
			}
		})
	}
}

func TestANSHook_Fire(t *testing.T) {
	SetErrorCategory(ErrorTest)
	defer SetErrorCategory(ErrorUndefined)
	type fields struct {
		levels       []logrus.Level
		defaultEvent ans.Event
		firing       bool
	}
	tests := []struct {
		name       string
		fields     fields
		entryArgs  []*logrus.Entry
		wantEvent  ans.Event
		wantErrMsg string
	}{
		{
			name:      "Straight forward test",
			fields:    fields{defaultEvent: defaultEvent()},
			entryArgs: []*logrus.Entry{defaultLogrusEntry()},
			wantEvent: defaultResultingEvent(),
		},
		{
			name: "Event already set",
			fields: fields{
				defaultEvent: mergeEvents(t, defaultEvent(), ans.Event{
					EventType: "My event type",
					Subject:   "My subject line",
					Tags:      map[string]interface{}{"Some": 1.0, "Additional": "a string", "Tags": true},
				}),
			},
			entryArgs: []*logrus.Entry{defaultLogrusEntry()},
			wantEvent: mergeEvents(t, defaultResultingEvent(), ans.Event{
				EventType: "My event type",
				Subject:   "My subject line",
				Tags:      map[string]interface{}{"Some": 1.0, "Additional": "a string", "Tags": true},
			}),
		},
		{
			name:   "Log entries should not affect each other",
			fields: fields{defaultEvent: defaultEvent()},
			entryArgs: []*logrus.Entry{
				{
					Level:   logrus.ErrorLevel,
					Time:    defaultTime.Add(1234),
					Message: "first log message",
					Data:    map[string]interface{}{"stepName": "testStep", "this entry": "should only be part of this event"},
				},
				defaultLogrusEntry(),
			},
			wantEvent: defaultResultingEvent(),
		},
		{
			name:   "White space messages should not send",
			fields: fields{defaultEvent: defaultEvent()},
			entryArgs: []*logrus.Entry{
				{
					Level:   logrus.ErrorLevel,
					Time:    defaultTime,
					Message: "   ",
					Data:    map[string]interface{}{"stepName": "testStep"},
				},
			},
		},
		{
			name:       "Should not fire twice",
			fields:     fields{firing: true, defaultEvent: defaultEvent()},
			entryArgs:  []*logrus.Entry{defaultLogrusEntry()},
			wantErrMsg: "ANS hook has already been fired",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientMock := ansMock{}
			ansHook := &ANSHook{
				client:        &clientMock,
				eventTemplate: tt.fields.defaultEvent,
				firing:        tt.fields.firing,
			}
			for _, entryArg := range tt.entryArgs {
				originalLogLevel := entryArg.Level
				if err := ansHook.Fire(entryArg); err != nil {
					assert.EqualError(t, err, tt.wantErrMsg)
				}
				assert.Equal(t, originalLogLevel.String(), entryArg.Level.String(), "Entry error level has been altered")
			}
			assert.Equal(t, tt.wantEvent, clientMock.event, "Event is not as expected.")
		})
	}
}

const testCorrelationID = "1234"
const defaultServiceKeyJSON = `{"url": "https://my.test.backend", "client_id": "myTestClientID", "client_secret": "super secret", "oauth_url": "https://my.test.oauth.provider"}`

var defaultTime = time.Date(2001, 2, 3, 4, 5, 6, 7, time.UTC)

func defaultCorrelationID() string {
	return testCorrelationID
}

func createConfiguration(events ...string) ans.Configuration {
	config := ans.Configuration{}
	if len(events) > 0 {
		config.EventTemplate = events[0]
	}
	return config
}

type FakeEvent ans.Event

var marshalAdditionalFields map[string]interface{}

func (b FakeEvent) MarshalJSON() ([]byte, error) {

	// m := make(map[string]interface{})
	// m["Event"] = ans.Event(b)

	// if marshalAdditionalFields != nil {
	// 	for key, value := range marshalAdditionalFields {
	// 		m[key] = value
	// 	}
	// }

	return json.Marshal(struct {
		ans.Event
	}{
		Event: ans.Event(b),
	})
}

func customerEventString(params ...interface{}) string {
	event := FakeEvent{
		EventType: "Piper",
		Tags:      map[string]interface{}{"ans:correlationId": testCorrelationID, "ans:sourceEventId": testCorrelationID},
		Resource: &ans.Resource{
			ResourceType: "Pipeline",
			ResourceName: "Pipeline",
		},
	}

	additionalFields := make(map[string]interface{})

	if len(params) > 0 {
		for i := 0; i < len(params); i++ {
			switch params[i].(type) {
			case map[string]interface{}:
				{
					m := params[i].(map[string]interface{})
					for key, value := range m {
						obj := reflect.Indirect(reflect.ValueOf(&event))
						if field := obj.FieldByName(key); field != (reflect.Value{}) {
							switch field.Kind() {
							case reflect.String:
								field.SetString(value.(string))
							case reflect.Int:
								switch value.(type) {
								case string:
									v, _ := strconv.Atoi(value.(string))
									field.SetInt(int64(v))
								case int:
									field.SetInt(int64((value).(int)))
								}
							}
						} else {
							additionalFields[key] = value
						}
					}
				}
			}
		}
	}

	// if len(additionalFields) > 0 {
	// 	marshalAdditionalFields = additionalFields
	// }

	b, err := json.Marshal(event)
	if err != nil {
		panic(fmt.Sprintf("cannot marshal customer event: %v", err))
	}
	// marshalAdditionalFields = nil

	if len(additionalFields) > 0 {
		closingBraceIdx := bytes.LastIndexByte(b, '}')
		for key, value := range additionalFields {
			var jvalue string
			switch value.(type) {
			case string:
				jvalue = value.(string)
			case int:
				jvalue = strconv.Itoa(value.(int))
			}
			entry := `, "` + key + `": "` + jvalue + `"`

			add := []byte(entry)
			b = append(b[:closingBraceIdx], add...)
		}
		b = append(b, '}')
	}

	return string(b)

}

func defaultEvent(params ...interface{}) ans.Event {
	event := ans.Event{
		EventType: "Piper",
		Tags:      map[string]interface{}{"ans:correlationId": testCorrelationID, "ans:sourceEventId": testCorrelationID},
		Resource: &ans.Resource{
			ResourceType: "Pipeline",
			ResourceName: "Pipeline",
		},
	}
	return event
}

func defaultResultingEvent() ans.Event {
	return ans.Event{
		EventType:      "Piper",
		EventTimestamp: defaultTime.Unix(),
		Severity:       "WARNING",
		Category:       "ALERT",
		Subject:        "testStep",
		Body:           "my log message",
		Resource: &ans.Resource{
			ResourceType: "Pipeline",
			ResourceName: "Pipeline",
		},
		Tags: map[string]interface{}{"ans:correlationId": "1234", "ans:sourceEventId": "1234", "stepName": "testStep", "logLevel": "warning", "errorCategory": "test"},
	}
}

func defaultLogrusEntry() *logrus.Entry {
	return &logrus.Entry{
		Level:   logrus.WarnLevel,
		Time:    defaultTime,
		Message: "my log message",
		Data:    map[string]interface{}{"stepName": "testStep"},
	}
}

func defaultANSClient() *ans.ANS {
	return &ans.ANS{
		XSUAA: xsuaa.XSUAA{
			OAuthURL:     "https://my.test.oauth.provider",
			ClientID:     "myTestClientID",
			ClientSecret: "super secret",
		},
		URL: "https://my.test.backend",
	}
}

func writeTempFile(t *testing.T, fileContent string) (fileName string) {
	var err error
	testEventTemplateFile, err := os.CreateTemp("", "event_template_*.json")
	require.NoError(t, err, "File creation failed!")
	defer testEventTemplateFile.Close()
	data := []byte(fileContent)
	_, err = testEventTemplateFile.Write(data)
	require.NoError(t, err, "Could not write test data to test file!")
	return testEventTemplateFile.Name()
}

func mergeEvents(t *testing.T, event1, event2 ans.Event) ans.Event {
	event2JSON, err := json.Marshal(event2)
	require.NoError(t, err)
	err = event1.MergeWithJSON(event2JSON)
	require.NoError(t, err)
	return event1
}

type ansMock struct {
	a        *ans.ANS
	event    ans.Event
	checkErr error
}

func (am *ansMock) Send(event ans.Event) error {
	am.event = event
	return nil
}

func (am *ansMock) CheckCorrectSetup() error {
	return am.checkErr
}

func (am *ansMock) SetServiceKey(serviceKey ans.ServiceKey) {
	a := &ans.ANS{}
	a.SetServiceKey(serviceKey)
	am.a = a
}
