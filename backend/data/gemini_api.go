package data

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"strings"
	"time"

	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"github.com/samber/lo"
)

type GeminiApi struct {
	ctx              context.Context
	BaseUrl          string  `json:"base_url"`
	ApiKey           string  `json:"api_key"`
	Model            string  `json:"model"`
	MaxTokens        int     `json:"max_tokens"`
	Temperature      float64 `json:"temperature"`
	TimeOut          int     `json:"time_out"`
	Prompt           string  `json:"prompt"`
	QuestionTemplate string  `json:"question_template"`
	CrawlTimeOut     int64   `json:"crawl_time_out"`
	KDays            int64   `json:"kDays"`
}

type GeminiRequest struct {
	Contents []*GeminiContent `json:"contents"`
}

type GeminiContent struct {
	Parts []*GeminiPart `json:"parts"`
	Role  string        `json:"role,omitempty"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiResponse struct {
	Candidates     []*GeminiCandidate    `json:"candidates"`
	PromptFeedback *GeminiPromptFeedback `json:"promptFeedback"`
}

type GeminiCandidate struct {
	Content       *GeminiContent        `json:"content"`
	FinishReason  string                `json:"finishReason"`
	Index         int                   `json:"index"`
	SafetyRatings []*GeminiSafetyRating `json:"safetyRatings"`
}

type GeminiPromptFeedback struct {
	SafetyRatings []*GeminiSafetyRating `json:"safetyRatings"`
}

type GeminiSafetyRating struct {
	Category    string `json:"category"`
	Probability string `json:"probability"`
}

func NewGeminiApi(ctx context.Context, aiConfigId int) *GeminiApi {
	settingConfig := GetSettingConfig()
	aiConfig, find := lo.Find(settingConfig.AiConfigs, func(item *AIConfig) bool {
		return uint(aiConfigId) == item.ID
	})
	if !find {
		aiConfig = &AIConfig{}
	}

	if aiConfig.TimeOut <= 0 {
		aiConfig.TimeOut = 60 * 5
	}
	if settingConfig.CrawlTimeOut <= 0 {
		settingConfig.CrawlTimeOut = 60
	}
	if settingConfig.KDays < 30 {
		settingConfig.KDays = 120
	}

	g := &GeminiApi{
		ctx:              ctx,
		BaseUrl:          aiConfig.BaseUrl,
		ApiKey:           aiConfig.ApiKey,
		Model:            aiConfig.ModelName,
		MaxTokens:        aiConfig.MaxTokens,
		Temperature:      aiConfig.Temperature,
		TimeOut:          aiConfig.TimeOut,
		Prompt:           settingConfig.Prompt,
		QuestionTemplate: settingConfig.QuestionTemplate,
		CrawlTimeOut:     settingConfig.CrawlTimeOut,
		KDays:            settingConfig.KDays,
	}
	return g
}

func (g *GeminiApi) NewGeminiSummaryStockNewsStream(userQuestion string, sysPromptId *int) <-chan map[string]any {
	ch := make(chan map[string]any, 512)
	go func() {
		defer close(ch)
		sysPrompt := g.Prompt
		if sysPromptId != nil && *sysPromptId != 0 {
			sysPrompt = NewPromptTemplateApi().GetPromptTemplateByID(*sysPromptId)
		}

		msg := []map[string]interface{}{
			{"role": "user", "content": sysPrompt}, // Gemini uses a different role system, starting with user
		}
		// The rest of the logic is to append 'model' and 'user' turns.
		// We will simulate this by adding to msg.

		// Simplified context gathering
		var contextBuilder strings.Builder
		contextBuilder.WriteString("Current Time: " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")

		// Market News
		news := NewMarketNewsApi().GetNewsList("", 100)
		for _, telegraph := range *news {
			contextBuilder.WriteString(fmt.Sprintf("## %s:\n### %s\n", telegraph.Time, telegraph.Content))
		}

		msg = append(msg, map[string]interface{}{"role": "model", "content": "I have the latest market news."})
		msg = append(msg, map[string]interface{}{"role": "user", "content": contextBuilder.String() + "\n\n" + userQuestion})

		AskGemini(g, nil, msg, ch, userQuestion)
	}()
	return ch
}

func (g *GeminiApi) NewGeminiChatStream(stock, stockCode, userQuestion string, sysPromptId *int) <-chan map[string]any {
	ch := make(chan map[string]any, 512)
	go func() {
		defer close(ch)
		sysPrompt := g.Prompt
		if sysPromptId != nil && *sysPromptId != 0 {
			sysPrompt = NewPromptTemplateApi().GetPromptTemplateByID(*sysPromptId)
		}

		msg := []map[string]interface{}{
			{"role": "user", "content": sysPrompt},
		}

		var contextBuilder strings.Builder
		contextBuilder.WriteString("Current Time: " + time.Now().Format("2006-01-02 15:04:05") + "\n\n")

		// Stock K-Line Data
		if strutil.HasPrefixAny(stockCode, []string{"sz", "sh", "hk", "us", "gb_"}) {
			var K *[]KLineData
			if strutil.HasPrefixAny(stockCode, []string{"sz", "sh"}) {
				K = NewStockDataApi().GetKLineData(stockCode, "240", g.KDays)
			} else {
				K = NewStockDataApi().GetHK_KLineData(stockCode, "day", g.KDays)
			}
			kmap := []map[string]any{}
			for _, kline := range *K {
				kmap = append(kmap, map[string]any{
					"日期":      kline.Day,
					"开盘价":     kline.Open,
					"最高价":     kline.High,
					"最低价":     kline.Low,
					"收盘价":     kline.Close,
					"成交量(万手)": kline.Volume,
				})
			}
			jsonData, _ := json.Marshal(kmap)
			markdownTable, _ := JSONToMarkdownTable(jsonData)
			contextBuilder.WriteString("## " + stock + " 日K数据如下：\n" + markdownTable + "\n\n")
		}

		// Financial Reports
		if !checkIsIndexBasic(stock) {
			messages := GetFinancialReportsByXUEQIU(stockCode, g.CrawlTimeOut)
			if len(*messages) > 0 {
				contextBuilder.WriteString((*messages)[0] + "\n\n")
			}
		}

		// Stock News
		newsMessages := SearchStockInfo(stock, "telegram", g.CrawlTimeOut)
		if len(*newsMessages) > 0 {
			contextBuilder.WriteString("## " + stock + " 相关新闻资讯\n" + strings.Join(*newsMessages, "\n") + "\n\n")
		}

		finalQuestion := userQuestion
		if finalQuestion == "" {
			finalQuestion = strings.NewReplacer("{{stockName}}", stock, "{{stockCode}}", stockCode).Replace(g.QuestionTemplate)
		}

		msg = append(msg, map[string]interface{}{"role": "model", "content": "I have the context for " + stock + "."})
		msg = append(msg, map[string]interface{}{"role": "user", "content": contextBuilder.String() + "\n\n" + finalQuestion})

		AskGemini(g, nil, msg, ch, finalQuestion)
	}()
	return ch
}

func (g *GeminiApi) StreamGenerateContent(prompt string) <-chan map[string]any {
	ch := make(chan map[string]any, 512)

	go func() {
		defer close(ch)

		client := resty.New()
		url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent", strutil.Trim(g.BaseUrl), g.Model)
		client.SetHeader("x-goog-api-key", g.ApiKey)
		client.SetHeader("Content-Type", "application/json")

		if g.TimeOut <= 0 {
			g.TimeOut = 300
		}
		client.SetTimeout(time.Duration(g.TimeOut) * time.Second)

		requestBody := &GeminiRequest{
			Contents: []*GeminiContent{
				{
					Parts: []*GeminiPart{
						{Text: prompt},
					},
				},
			},
		}

		resp, err := client.R().
			SetDoNotParseResponse(true).
			SetBody(requestBody).
			Post(url)

		if err != nil {
			logger.SugaredLogger.Errorf("Gemini stream error: %s", err.Error())
			ch <- map[string]any{"error": err.Error()}
			return
		}
		defer resp.RawBody().Close()

		scanner := bufio.NewScanner(resp.RawBody())
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				var streamResponse GeminiResponse
				if err := json.Unmarshal([]byte(data), &streamResponse); err == nil {
					if len(streamResponse.Candidates) > 0 && streamResponse.Candidates[0].Content != nil && len(streamResponse.Candidates[0].Content.Parts) > 0 {
						content := streamResponse.Candidates[0].Content.Parts[0].Text
						ch <- map[string]any{"content": content}
					}
				}
			}
		}

		if err := scanner.Err(); err != nil {
			logger.SugaredLogger.Errorf("Error reading stream from Gemini: %v", err)
		}
	}()

	return ch
}

// SaveAIResponseResult for Gemini (can be adapted if needed)
func (g *GeminiApi) SaveAIResponseResult(stockCode, stockName, result, chatId, question string) {
	db.Dao.Create(&models.AIResponseResult{
		StockCode: stockCode,
		StockName: stockName,
		ModelName: g.Model,
		Content:   result,
		ChatId:    chatId,
		Question:  question,
	})
}

// GetAIResponseResult for Gemini (can be adapted if needed)
func (g *GeminiApi) GetAIResponseResult(stock string) *models.AIResponseResult {
	res := &models.AIResponseResult{}
	db.Dao.Model(res).Where("stock_code = ? or stock_name = ?", stock, stock).Order("created_at desc").First(res)
	return res
}

func AskGemini(g *GeminiApi, err error, messages []map[string]interface{}, ch chan map[string]any, question string) {
	client := resty.New()
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent", strutil.Trim(g.BaseUrl), g.Model)
	client.SetHeader("x-goog-api-key", g.ApiKey)
	client.SetHeader("Content-Type", "application/json")

	if g.TimeOut <= 0 {
		g.TimeOut = 300
	}
	client.SetTimeout(time.Duration(g.TimeOut) * time.Second)

	geminiContents := []*GeminiContent{}
	for _, msg := range messages {
		role, _ := msg["role"].(string)
		content, _ := msg["content"].(string)
		geminiRole := "user"
		if role != "user" {
			geminiRole = "model"
		}
		geminiContents = append(geminiContents, &GeminiContent{
			Role:  geminiRole,
			Parts: []*GeminiPart{{Text: content}},
		})
	}

	requestBody := &GeminiRequest{
		Contents: geminiContents,
	}
	requestJson, _ := json.Marshal(requestBody)
	logger.SugaredLogger.Infof("Gemini request URL: %s", url)
	logger.SugaredLogger.Infof("Gemini request body: %s", string(requestJson))

	resp, err := client.R().
		SetDoNotParseResponse(true).
		SetBody(requestBody).
		Post(url)

	if err != nil {
		logger.SugaredLogger.Infof("Stream error : %s", err.Error())
		ch <- map[string]any{"code": 0, "question": question, "content": err.Error()}
		return
	}
	defer resp.RawBody().Close()

	scanner := bufio.NewScanner(resp.RawBody())
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var streamResponse GeminiResponse
			if err := json.Unmarshal([]byte(data), &streamResponse); err == nil {
				if len(streamResponse.Candidates) > 0 && streamResponse.Candidates[0].Content != nil && len(streamResponse.Candidates[0].Content.Parts) > 0 {
					content := streamResponse.Candidates[0].Content.Parts[0].Text
					ch <- map[string]any{
						"code":     1,
						"question": question,
						"chatId":   "gemini-response",
						"model":    g.Model,
						"content":  content,
						"time":     time.Now().Format(time.DateTime),
					}
				}
			}
		}
	}
}
