package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"go-stock/backend/models"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/duke-git/lancet/v2/cryptor"
	"github.com/inconshreveable/go-update"

	"github.com/PuerkitoBio/goquery"
	"github.com/coocood/freecache"
	"github.com/duke-git/lancet/v2/convertor"
	"github.com/duke-git/lancet/v2/mathutil"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/duke-git/lancet/v2/strutil"
	"github.com/go-resty/resty/v2"
	"github.com/robfig/cron/v3"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx         context.Context
	cache       *freecache.Cache
	cron        *cron.Cron
	cronEntrys  map[string]cron.EntryID
	AiTools     []data.Tool
	SponsorInfo map[string]any
}

// NewApp creates a new App application struct
func NewApp() *App {
	cacheSize := 512 * 1024
	cache := freecache.NewCache(cacheSize)
	c := cron.New(cron.WithSeconds())
	c.Start()
	var tools []data.Tool
	tools = AddTools(tools)
	return &App{
		cache:      cache,
		cron:       c,
		cronEntrys: make(map[string]cron.EntryID),
		AiTools:    tools,
	}
}

func AddTools(tools []data.Tool) []data.Tool {
	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "SearchStockByIndicators",
			Description: "根据自然语言筛选股票，返回自然语言选股条件要求的股票所有相关数据。输入股票名称可以获取当前股票最新的股价交易数据和基础财务指标信息，多个股票名称使用,分隔。",
			Parameters: data.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"words": map[string]any{
						"type": "string",
						"description": "选股自然语言。" +
							"例1：创新药,半导体;PE<30;净利润增长率>50%。 " +
							"例2：上证指数,科创50。 " +
							"例3：长电科技,上海贝岭。" +
							"例4：长电科技,上海贝岭;KDJ,MACD,RSI,BOLL,主力净流入/流出" +
							"例5：换手率大于3%小于25%.量比1以上. 10日内有过涨停.股价处于峰值的二分之一以下.流通股本<100亿.当日和连续四日净流入;股价在20日均线以上.分时图股价在均线之上.热门板块下涨幅领先的A股. 当日量能20000手以上.沪深个股.近一年市盈率波动小于150%.MACD金叉;不要ST股及不要退市股，非北交所，每股收益>0。" +
							"例6：沪深主板.流通市值小于100亿.市值大于10亿.60分钟dif大于dea.60分钟skdj指标k值大于d值.skdj指标k值小于90.换手率大于3%.成交额大于1亿元.量比大于2.涨幅大于2%小于7%.股价大于5小于50.创业板.10日均线大于20日均线;不要ST股及不要退市股;不要北交所;不要科创板;不要创业板。" +
							"例7：股价在20日线上，一月之内涨停次数>=1，量比大于1，换手率大于3%，流通市值大于 50亿小于200亿。" +
							"例8：基本条件：前期有爆量，回调到 10 日线，当日是缩量阴线，均线趋势向上。;优选条件：一月之内涨停次数>=1",
					},
				},
				Required: []string{"words"},
			},
		},
	})

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "GetStockKLine",
			Description: "获取股票日K线数据。",
			Parameters: data.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"days": map[string]any{
						"type":        "string",
						"description": "日K数据条数",
					},
					"stockCode": map[string]any{
						"type":        "string",
						"description": "股票代码（A股：sh,sz开头;港股hk开头,美股：us开头）",
					},
				},
				Required: []string{"days", "stockCode"},
			},
		},
	})

	tools = append(tools, data.Tool{
		Type: "function",
		Function: data.ToolFunction{
			Name:        "InteractiveAnswer",
			Description: "获取投资者与上市公司互动问答的数据,反映当前投资者关注的热点问题",
			Parameters: data.FunctionParameters{
				Type: "object",
				Properties: map[string]any{
					"page": map[string]any{
						"type":        "string",
						"description": "分页号",
					},
					"pageSize": map[string]any{
						"type":        "string",
						"description": "分页大小",
					},
					"keyWord": map[string]any{
						"type":        "string",
						"description": "搜索关键词（可输入股票名称或者当前热门板块/行业/概念/标的/事件等）",
					},
				},
				Required: []string{"page", "pageSize"},
			},
		},
	})

	return tools
}

func (a *App) GetSponsorInfo() map[string]any {
	return a.SponsorInfo
}
func (a *App) CheckSponsorCode(sponsorCode string) map[string]any {
	sponsorCode = strutil.Trim(sponsorCode)
	if sponsorCode != "" {
		encrypted, err := hex.DecodeString(sponsorCode)
		if err != nil {
			return map[string]any{
				"code": 0,
				"msg":  "赞助码格式错误,请输入正确的赞助码!",
			}
		}
		key, err := hex.DecodeString(BuildKey)
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
			return map[string]any{
				"code": 0,
				"msg":  "版本错误，不支持赞助码!",
			}
		}
		decrypt := cryptor.AesEcbDecrypt(encrypted, key)
		if decrypt == nil || len(decrypt) == 0 {
			return map[string]any{
				"code": 0,
				"msg":  "赞助码错误，请输入正确的赞助码!",
			}
		}
		return map[string]any{
			"code": 1,
			"msg":  "赞助码校验成功，感谢您的支持!",
		}
	} else {
		return map[string]any{"code": 0, "message": "赞助码不能为空,请输入正确的赞助码!"}
	}
}

func (a *App) CheckUpdate(flag int) {
	sponsorCode := strutil.Trim(a.GetConfig().SponsorCode)
	if sponsorCode != "" {
		encrypted, err := hex.DecodeString(sponsorCode)
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
			return
		}
		key, err := hex.DecodeString(BuildKey)
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
			return
		}
		decrypt := string(cryptor.AesEcbDecrypt(encrypted, key))
		err = json.Unmarshal([]byte(decrypt), &a.SponsorInfo)
		if err != nil {
			logger.SugaredLogger.Error(err.Error())
			return
		}
	}

	releaseVersion := &models.GitHubReleaseVersion{}
	_, err := resty.New().R().
		SetResult(releaseVersion).
		Get("https://api.github.com/repos/ArvinLovegood/go-stock/releases/latest")
	if err != nil {
		logger.SugaredLogger.Errorf("get github release version error:%s", err.Error())
		return
	}
	logger.SugaredLogger.Infof("releaseVersion:%+v", releaseVersion.TagName)
	if releaseVersion.TagName != Version {

		tag := &models.Tag{}
		_, err = resty.New().R().
			SetResult(tag).
			Get("https://api.github.com/repos/ArvinLovegood/go-stock/git/ref/tags/" + releaseVersion.TagName)
		if err == nil {
			releaseVersion.Tag = *tag
		}

		commit := &models.Commit{}
		_, err = resty.New().R().
			SetResult(commit).
			Get(tag.Object.Url)
		if err == nil {
			releaseVersion.Commit = *commit
		}

		if !(IsWindows() || IsMacOS()) {
			go runtime.EventsEmit(a.ctx, "updateVersion", releaseVersion)
			return
		}
		downloadUrl := fmt.Sprintf("https://github.com/ArvinLovegood/go-stock/releases/download/%s/go-stock-windows-amd64.exe", releaseVersion.TagName)
		if IsMacOS() {
			downloadUrl = fmt.Sprintf("https://github.com/ArvinLovegood/go-stock/releases/download/%s/go-stock-darwin-universal", releaseVersion.TagName)
		}
		sponsorCode = strutil.Trim(a.GetConfig().SponsorCode)
		if sponsorCode != "" {
			encrypted, err := hex.DecodeString(sponsorCode)
			if err != nil {
				logger.SugaredLogger.Error(err.Error())
				return
			}
			key, err := hex.DecodeString(BuildKey)
			if err != nil {
				logger.SugaredLogger.Error(err.Error())
				return
			}
			decrypt := string(cryptor.AesEcbDecrypt(encrypted, key))
			err = json.Unmarshal([]byte(decrypt), &a.SponsorInfo)
			if err != nil {
				logger.SugaredLogger.Error(err.Error())
				return
			}
			vipStartTime, err := time.ParseInLocation("2006-01-02 15:04:05", a.SponsorInfo["vipStartTime"].(string), time.Local)
			vipEndTime, err := time.ParseInLocation("2006-01-02 15:04:05", a.SponsorInfo["vipEndTime"].(string), time.Local)
			vipAuthTime, err := time.ParseInLocation("2006-01-02 15:04:05", a.SponsorInfo["vipAuthTime"].(string), time.Local)
			if err != nil {
				logger.SugaredLogger.Error(err.Error())
				return
			}
			isVip := false

			if time.Now().After(vipAuthTime) && time.Now().After(vipStartTime) && time.Now().Before(vipEndTime) {
				isVip = true
			}

			if IsWindows() {
				if isVip {
					if a.SponsorInfo["winDownUrl"] == nil {
						downloadUrl = fmt.Sprintf("https://gitproxy.click/https://github.com/ArvinLovegood/go-stock/releases/download/%s/go-stock-windows-amd64.exe", releaseVersion.TagName)
					} else {
						downloadUrl = a.SponsorInfo["winDownUrl"].(string)
					}
				} else {
					downloadUrl = fmt.Sprintf("https://github.com/ArvinLovegood/go-stock/releases/download/%s/go-stock-windows-amd64.exe", releaseVersion.TagName)
				}
			}
			if IsMacOS() {
				if isVip {
					if a.SponsorInfo["macDownUrl"] == nil {
						downloadUrl = fmt.Sprintf("https://gitproxy.click/https://github.com/ArvinLovegood/go-stock/releases/download/%s/go-stock-darwin-universal", releaseVersion.TagName)
					} else {
						downloadUrl = a.SponsorInfo["macDownUrl"].(string)
					}
				} else {
					downloadUrl = fmt.Sprintf("https://github.com/ArvinLovegood/go-stock/releases/download/%s/go-stock-darwin-universal", releaseVersion.TagName)
				}
			}
		}
		go runtime.EventsEmit(a.ctx, "newsPush", map[string]any{
			"time":    "发现新版本：" + releaseVersion.TagName,
			"isRed":   true,
			"source":  "go-stock",
			"content": fmt.Sprintf("%s", commit.Message),
		})
		resp, err := resty.New().R().Get(downloadUrl)
		if err != nil {
			go runtime.EventsEmit(a.ctx, "newsPush", map[string]any{
				"time":    "新版本：" + releaseVersion.TagName,
				"isRed":   true,
				"source":  "go-stock",
				"content": commit.Message + "\n新版本下载失败,请稍后重试或请前往 https://github.com/ArvinLovegood/go-stock/releases 手动下载替换文件。",
			})
			return
		}
		body := resp.Body()

		if len(body) < 1024*500 {
			go runtime.EventsEmit(a.ctx, "newsPush", map[string]any{
				"time":    "新版本：" + releaseVersion.TagName,
				"isRed":   true,
				"source":  "go-stock",
				"content": commit.Message + "\n新版本下载失败,请稍后重试或请前往 https://github.com/ArvinLovegood/go-stock/releases 手动下载替换文件。",
			})
			return
		}

		err = update.Apply(bytes.NewReader(body), update.Options{})
		if err != nil {
			logger.SugaredLogger.Error("更新失败: ", err.Error())
			go runtime.EventsEmit(a.ctx, "updateVersion", releaseVersion)
			return
		} else {
			go runtime.EventsEmit(a.ctx, "newsPush", map[string]any{
				"time":    "新版本：" + releaseVersion.TagName,
				"isRed":   true,
				"source":  "go-stock",
				"content": "版本更新完成,下次重启软件生效.",
			})
		}
	} else {
		if flag == 1 {
			go runtime.EventsEmit(a.ctx, "newsPush", map[string]any{
				"time":    "当前版本：" + Version,
				"isRed":   true,
				"source":  "go-stock",
				"content": "当前版本无更新",
			})
		}

	}
}

// domReady is called after front-end resources have been loaded
func (a *App) domReady(ctx context.Context) {
	defer PanicHandler()
	defer func() {
		// 增加延迟确保前端已准备好接收事件
		go func() {
			time.Sleep(2 * time.Second)
			runtime.EventsEmit(a.ctx, "loadingMsg", "done")
		}()
	}()

	//if stocksBin != nil && len(stocksBin) > 0 {
	//	go runtime.EventsEmit(a.ctx, "loadingMsg", "检查A股基础信息...")
	//	go initStockData(a.ctx)
	//}
	//
	//if stocksBinHK != nil && len(stocksBinHK) > 0 {
	//	go runtime.EventsEmit(a.ctx, "loadingMsg", "检查港股基础信息...")
	//	go initStockDataHK(a.ctx)
	//}
	//
	//if stocksBinUS != nil && len(stocksBinUS) > 0 {
	//	go runtime.EventsEmit(a.ctx, "loadingMsg", "检查美股基础信息...")
	//	go initStockDataUS(a.ctx)
	//}
	updateBasicInfo()

	// Add your action here
	//定时更新数据
	config := data.GetSettingConfig()
	go func() {
		interval := config.RefreshInterval
		if interval <= 0 {
			interval = 1
		}
		//ticker := time.NewTicker(time.Second * time.Duration(interval))
		//defer ticker.Stop()
		//for range ticker.C {
		//	MonitorStockPrices(a)
		//}
		id, err := a.cron.AddFunc(fmt.Sprintf("@every %ds", interval), func() {
			MonitorStockPrices(a)
		})
		if err != nil {
			logger.SugaredLogger.Errorf("AddFunc error:%s", err.Error())
		} else {
			a.cronEntrys["MonitorStockPrices"] = id
		}
		entryID, err := a.cron.AddFunc(fmt.Sprintf("@every %ds", interval+10), func() {
			news := data.NewMarketNewsApi().GetNewTelegraph(30)
			if config.EnablePushNews {
				go a.NewsPush(news)
			}
			go runtime.EventsEmit(a.ctx, "newTelegraph", news)
		})
		if err != nil {
			logger.SugaredLogger.Errorf("AddFunc error:%s", err.Error())
		} else {
			a.cronEntrys["GetNewTelegraph"] = entryID
		}

		entryIDSina, err := a.cron.AddFunc(fmt.Sprintf("@every %ds", interval+10), func() {
			news := data.NewMarketNewsApi().GetSinaNews(30)
			if config.EnablePushNews {
				go a.NewsPush(news)
			}
			go runtime.EventsEmit(a.ctx, "newSinaNews", news)
		})
		if err != nil {
			logger.SugaredLogger.Errorf("AddFunc error:%s", err.Error())
		} else {
			a.cronEntrys["newSinaNews"] = entryIDSina
		}
	}()

	//刷新基金净值信息
	go func() {
		//ticker := time.NewTicker(time.Second * time.Duration(60))
		//defer ticker.Stop()
		//for range ticker.C {
		//	MonitorFundPrices(a)
		//}
		if config.EnableFund {
			id, err := a.cron.AddFunc(fmt.Sprintf("@every %ds", 60), func() {
				MonitorFundPrices(a)
			})
			if err != nil {
				logger.SugaredLogger.Errorf("AddFunc error:%s", err.Error())
			} else {
				a.cronEntrys["MonitorFundPrices"] = id
			}
		}

	}()

	if config.EnableNews {
		//go func() {
		//	ticker := time.NewTicker(time.Second * time.Duration(60))
		//	defer ticker.Stop()
		//	for range ticker.C {
		//		telegraph := refreshTelegraphList()
		//		if telegraph != nil {
		//			go runtime.EventsEmit(a.ctx, "telegraph", telegraph)
		//		}
		//	}
		//
		//}()

		id, err := a.cron.AddFunc(fmt.Sprintf("@every %ds", 60), func() {
			telegraph := refreshTelegraphList()
			if telegraph != nil {
				go runtime.EventsEmit(a.ctx, "telegraph", telegraph)
			}
		})
		if err != nil {
			logger.SugaredLogger.Errorf("AddFunc error:%s", err.Error())
		} else {
			a.cronEntrys["refreshTelegraphList"] = id
		}

		go runtime.EventsEmit(a.ctx, "telegraph", refreshTelegraphList())
	}
	go MonitorStockPrices(a)
	if config.EnableFund {
		go MonitorFundPrices(a)
		go data.NewFundApi().AllFund()
	}
	//检查新版本
	go func() {
		a.CheckUpdate(0)
		go a.CheckStockBaseInfo(a.ctx)

		a.cron.AddFunc("0 0 2 * * *", func() {
			logger.SugaredLogger.Errorf("Checking for updates...")
			a.CheckStockBaseInfo(a.ctx)
		})
		a.cron.AddFunc("30 05 8,12,20 * * *", func() {
			logger.SugaredLogger.Errorf("Checking for updates...")
			a.CheckUpdate(0)
		})
	}()

	//检查谷歌浏览器
	//go func() {
	//	f := checkChromeOnWindows()
	//	if !f {
	//		go runtime.EventsEmit(a.ctx, "warnMsg", "谷歌浏览器未安装,ai分析功能可能无法使用")
	//	}
	//}()

	//检查Edge浏览器
	//go func() {
	//	path, e := checkEdgeOnWindows()
	//	if !e {
	//		go runtime.EventsEmit(a.ctx, "warnMsg", "Edge浏览器未安装,ai分析功能可能无法使用")
	//	} else {
	//		logger.SugaredLogger.Infof("Edge浏览器已安装，路径为: %s", path)
	//	}
	//}()
	followList := data.NewStockDataApi().GetFollowList(0)
	for _, follow := range *followList {
		if follow.Cron == nil || *follow.Cron == "" {
			continue
		}
		entryID, err := a.cron.AddFunc(*follow.Cron, a.AddCronTask(follow))
		if err != nil {
			logger.SugaredLogger.Errorf("添加自动分析任务失败:%s cron=%s entryID:%v", follow.Name, *follow.Cron, entryID)
			continue
		}
		a.cronEntrys[follow.StockCode] = entryID
	}
	logger.SugaredLogger.Infof("domReady-cronEntrys:%+v", a.cronEntrys)

}
func (a *App) CheckStockBaseInfo(ctx context.Context) {
	defer PanicHandler()
	defer func() {
		go runtime.EventsEmit(ctx, "loadingMsg", "done")
	}()
	stockBasics := &[]data.StockBasic{}
	resty.New().R().
		SetHeader("user", "go-stock").
		SetResult(stockBasics).
		Get("http://8.134.249.145:18080/go-stock/stock_basic.json")

	count := int64(0)
	db.Dao.Model(&data.StockBasic{}).Count(&count)
	if count == int64(len(*stockBasics)) {
		return
	}
	for _, stock := range *stockBasics {
		stockInfo := &data.StockBasic{
			TsCode: stock.TsCode,
			Name:   stock.Name,
			Symbol: stock.Symbol,
			BKCode: stock.BKCode,
			BKName: stock.BKName,
		}
		db.Dao.Model(&data.StockBasic{}).Where("ts_code = ?", stock.TsCode).First(stockInfo)
		if stockInfo.ID == 0 {
			db.Dao.Model(&data.StockBasic{}).Create(stockInfo)
		} else {
			db.Dao.Model(&data.StockBasic{}).Where("ts_code = ?", stock.TsCode).Updates(stockInfo)
		}
	}

	stockHKBasics := &[]models.StockInfoHK{}
	resty.New().R().
		SetHeader("user", "go-stock").
		SetResult(stockHKBasics).
		Get("http://8.134.249.145:18080/go-stock/stock_base_info_hk.json")
	for _, stock := range *stockHKBasics {
		stockInfo := &models.StockInfoHK{
			Code:   stock.Code,
			Name:   stock.Name,
			BKName: stock.BKName,
			BKCode: stock.BKCode,
		}
		db.Dao.Model(&models.StockInfoHK{}).Where("code = ?", stock.Code).First(stockInfo)
		if stockInfo.ID == 0 {
			db.Dao.Model(&models.StockInfoHK{}).Create(stockInfo)
		} else {
			db.Dao.Model(&models.StockInfoHK{}).Where("code = ?", stock.Code).Updates(stockInfo)
		}
	}
	stockUSBasics := &[]models.StockInfoUS{}
	resty.New().R().
		SetHeader("user", "go-stock").
		SetResult(stockUSBasics).
		Get("http://8.134.249.145:18080/go-stock/stock_base_info_us.json")
	for _, stock := range *stockUSBasics {
		stockInfo := &models.StockInfoUS{
			Code:   stock.Code,
			Name:   stock.Name,
			BKName: stock.BKName,
			BKCode: stock.BKCode,
		}
		db.Dao.Model(&models.StockInfoUS{}).Where("code = ?", stock.Code).First(stockInfo)
		if stockInfo.ID == 0 {
			db.Dao.Model(&models.StockInfoUS{}).Create(stockInfo)
		} else {
			db.Dao.Model(&models.StockInfoUS{}).Where("code = ?", stock.Code).Updates(stockInfo)
		}
	}

}
func (a *App) NewsPush(news *[]models.Telegraph) {

	follows := data.NewStockDataApi().GetFollowList(0)
	stockNames := slice.Map(*follows, func(index int, item data.FollowedStock) string {
		return item.Name
	})

	for _, telegraph := range *news {
		if a.GetConfig().EnableOnlyPushRedNews {
			if telegraph.IsRed || strutil.ContainsAny(telegraph.Content, stockNames) {
				go runtime.EventsEmit(a.ctx, "newsPush", telegraph)
			}
		} else {
			go runtime.EventsEmit(a.ctx, "newsPush", telegraph)
		}
		//go data.NewAlertWindowsApi("go-stock", telegraph.Source+" "+telegraph.Time, telegraph.Content, string(icon)).SendNotification()
		//}
	}
}

func (a *App) AddCronTask(follow data.FollowedStock) func() {
	return func() {
		go runtime.EventsEmit(a.ctx, "warnMsg", "开始自动分析"+follow.Name+"_"+follow.StockCode)
		ai := data.NewDeepSeekOpenAi(a.ctx, follow.AiConfigId)
		msgs := ai.NewChatStream(follow.Name, follow.StockCode, "", nil, a.AiTools)
		var res strings.Builder

		chatId := ""
		question := ""
		for msg := range msgs {
			if msg["extraContent"] != nil {
				res.WriteString(msg["extraContent"].(string) + "\n")
			}
			if msg["content"] != nil {
				res.WriteString(msg["content"].(string))
			}
			if msg["chatId"] != nil {
				chatId = msg["chatId"].(string)
			}
			if msg["question"] != nil {
				question = msg["question"].(string)
			}
		}

		data.NewDeepSeekOpenAi(a.ctx, follow.AiConfigId).SaveAIResponseResult(follow.StockCode, follow.Name, res.String(), chatId, question)
		go runtime.EventsEmit(a.ctx, "warnMsg", "AI分析完成："+follow.Name+"_"+follow.StockCode)

	}
}

func refreshTelegraphList() *[]string {
	url := "https://www.cls.cn/telegraph"
	response, err := resty.New().R().
		SetHeader("Referer", "https://www.cls.cn/").
		SetHeader("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/117.0.0.0 Safari/537.36 Edg/117.0.2045.60").
		Get(fmt.Sprintf(url))
	if err != nil {
		return &[]string{}
	}
	//logger.SugaredLogger.Info(string(response.Body()))
	document, err := goquery.NewDocumentFromReader(strings.NewReader(string(response.Body())))
	if err != nil {
		return &[]string{}
	}
	var telegraph []string
	document.Find("div.telegraph-content-box").Each(func(i int, selection *goquery.Selection) {
		//logger.SugaredLogger.Info(selection.Text())
		telegraph = append(telegraph, selection.Text())
	})
	return &telegraph
}

// isTradingDay 判断是否是交易日
func isTradingDay(date time.Time) bool {
	weekday := date.Weekday()
	// 判断是否是周末
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}
	// 这里可以添加具体的节假日判断逻辑
	// 例如：判断是否是春节、国庆节等
	return true
}

// isTradingTime 判断是否是交易时间
func isTradingTime(date time.Time) bool {
	if !isTradingDay(date) {
		return false
	}

	hour, minute, _ := date.Clock()

	// 判断是否在9:15到11:30之间
	if (hour == 9 && minute >= 15) || (hour == 10) || (hour == 11 && minute <= 30) {
		return true
	}

	// 判断是否在13:00到15:00之间
	if (hour == 13) || (hour == 14) || (hour == 15 && minute <= 0) {
		return true
	}

	return false
}

// IsHKTradingTime 判断当前时间是否在港股交易时间内
func IsHKTradingTime(date time.Time) bool {
	hour, minute, _ := date.Clock()

	// 开市前竞价时段：09:00 - 09:30
	if (hour == 9 && minute >= 0) || (hour == 9 && minute <= 30) {
		return true
	}

	// 上午持续交易时段：09:30 - 12:00
	if (hour == 9 && minute > 30) || (hour >= 10 && hour < 12) || (hour == 12 && minute == 0) {
		return true
	}

	// 下午持续交易时段：13:00 - 16:00
	if (hour == 13 && minute >= 0) || (hour >= 14 && hour < 16) || (hour == 16 && minute == 0) {
		return true
	}

	// 收市竞价交易时段：16:00 - 16:10
	if (hour == 16 && minute >= 0) || (hour == 16 && minute <= 10) {
		return true
	}
	return false
}

// IsUSTradingTime 判断当前时间是否在美股交易时间内
func IsUSTradingTime(date time.Time) bool {
	// 获取美国东部时区
	est, err := time.LoadLocation("America/New_York")
	var estTime time.Time
	if err != nil {
		estTime = date.Add(time.Hour * -12)
	} else {
		// 将当前时间转换为美国东部时间
		estTime = date.In(est)
	}

	// 判断是否是周末
	weekday := estTime.Weekday()
	if weekday == time.Saturday || weekday == time.Sunday {
		return false
	}

	// 获取小时和分钟
	hour, minute, _ := estTime.Clock()

	// 判断是否在4:00 AM到9:30 AM之间（盘前）
	if (hour == 4) || (hour == 5) || (hour == 6) || (hour == 7) || (hour == 8) || (hour == 9 && minute < 30) {
		return true
	}

	// 判断是否在9:30 AM到4:00 PM之间（盘中）
	if (hour == 9 && minute >= 30) || (hour >= 10 && hour < 16) || (hour == 16 && minute == 0) {
		return true
	}

	// 判断是否在4:00 PM到8:00 PM之间（盘后）
	if (hour == 16 && minute > 0) || (hour >= 17 && hour < 20) || (hour == 20 && minute == 0) {
		return true
	}

	return false
}
func MonitorFundPrices(a *App) {
	dest := &[]data.FollowedFund{}
	db.Dao.Model(&data.FollowedFund{}).Find(dest)
	for _, follow := range *dest {
		_, err := data.NewFundApi().CrawlFundBasic(follow.Code)
		if err != nil {
			logger.SugaredLogger.Errorf("获取基金基本信息失败，基金代码：%s，错误信息：%s", follow.Code, err.Error())
			continue
		}
		data.NewFundApi().CrawlFundNetEstimatedUnit(follow.Code)
		data.NewFundApi().CrawlFundNetUnitValue(follow.Code)
	}
}

func GetStockInfos(follows ...data.FollowedStock) *[]data.StockInfo {
	stockInfos := make([]data.StockInfo, 0)
	stockCodes := make([]string, 0)
	for _, follow := range follows {
		if strutil.HasPrefixAny(follow.StockCode, []string{"SZ", "SH", "sh", "sz"}) && (!isTradingTime(time.Now())) {
			continue
		}
		if strutil.HasPrefixAny(follow.StockCode, []string{"hk", "HK"}) && (!IsHKTradingTime(time.Now())) {
			continue
		}
		if strutil.HasPrefixAny(follow.StockCode, []string{"us", "US", "gb_"}) && (!IsUSTradingTime(time.Now())) {
			continue
		}
		stockCodes = append(stockCodes, follow.StockCode)
	}
	stockData, _ := data.NewStockDataApi().GetStockCodeRealTimeData(stockCodes...)
	for _, info := range *stockData {
		v, ok := slice.FindBy(follows, func(idx int, follow data.FollowedStock) bool {
			if strutil.HasPrefixAny(follow.StockCode, []string{"US", "us"}) {
				return strings.ToLower(strings.Replace(follow.StockCode, "us", "gb_", 1)) == info.Code
			}

			return follow.StockCode == info.Code
		})
		if ok {
			addStockFollowData(v, &info)
			stockInfos = append(stockInfos, info)
		}
	}
	return &stockInfos
}
func getStockInfo(follow data.FollowedStock) *data.StockInfo {
	stockCode := follow.StockCode
	stockDatas, err := data.NewStockDataApi().GetStockCodeRealTimeData(stockCode)
	if err != nil || len(*stockDatas) == 0 {
		return &data.StockInfo{}
	}
	stockData := (*stockDatas)[0]
	addStockFollowData(follow, &stockData)
	return &stockData
}

func addStockFollowData(follow data.FollowedStock, stockData *data.StockInfo) {
	stockData.PrePrice = follow.Price //上次当前价格
	stockData.Sort = follow.Sort
	stockData.CostPrice = follow.CostPrice //成本价
	stockData.CostVolume = follow.Volume   //成本量
	stockData.AlarmChangePercent = follow.AlarmChangePercent
	stockData.AlarmPrice = follow.AlarmPrice
	stockData.Groups = follow.Groups

	//当前价格
	price, _ := convertor.ToFloat(stockData.Price)
	//当前价格为0 时 使用卖一价格作为当前价格
	if price == 0 {
		price, _ = convertor.ToFloat(stockData.A1P)
	}
	//当前价格依然为0 时 使用买一报价作为当前价格
	if price == 0 {
		price, _ = convertor.ToFloat(stockData.B1P)
	}

	//昨日收盘价
	preClosePrice, _ := convertor.ToFloat(stockData.PreClose)

	//当前价格依然为0 时 使用昨日收盘价为当前价格
	if price == 0 {
		price = preClosePrice
	}

	//今日最高价
	highPrice, _ := convertor.ToFloat(stockData.High)
	if highPrice == 0 {
		highPrice, _ = convertor.ToFloat(stockData.Open)
	}

	//今日最低价
	lowPrice, _ := convertor.ToFloat(stockData.Low)
	if lowPrice == 0 {
		lowPrice, _ = convertor.ToFloat(stockData.Open)
	}
	//开盘价
	//openPrice, _ := convertor.ToFloat(stockData.Open)

	if price > 0 && preClosePrice > 0 {
		stockData.ChangePrice = mathutil.RoundToFloat(price-preClosePrice, 2)
		stockData.ChangePercent = mathutil.RoundToFloat(mathutil.Div(price-preClosePrice, preClosePrice)*100, 3)
	}
	if highPrice > 0 && preClosePrice > 0 {
		stockData.HighRate = mathutil.RoundToFloat(mathutil.Div(highPrice-preClosePrice, preClosePrice)*100, 3)
	}
	if lowPrice > 0 && preClosePrice > 0 {
		stockData.LowRate = mathutil.RoundToFloat(mathutil.Div(lowPrice-preClosePrice, preClosePrice)*100, 3)
	}
	if follow.CostPrice > 0 && follow.Volume > 0 {
		if price > 0 {
			stockData.Profit = mathutil.RoundToFloat(mathutil.Div(price-follow.CostPrice, follow.CostPrice)*100, 3)
			stockData.ProfitAmount = mathutil.RoundToFloat((price-follow.CostPrice)*float64(follow.Volume), 2)
			stockData.ProfitAmountToday = mathutil.RoundToFloat((price-preClosePrice)*float64(follow.Volume), 2)
		} else {
			//未开盘时当前价格为昨日收盘价
			stockData.Profit = mathutil.RoundToFloat(mathutil.Div(preClosePrice-follow.CostPrice, follow.CostPrice)*100, 3)
			stockData.ProfitAmount = mathutil.RoundToFloat((preClosePrice-follow.CostPrice)*float64(follow.Volume), 2)
			// 未开盘时，今日盈亏为 0
			stockData.ProfitAmountToday = 0
		}

	}

	//logger.SugaredLogger.Debugf("stockData:%+v", stockData)
	if follow.Price != price && price > 0 {
		go db.Dao.Model(follow).Where("stock_code = ?", follow.StockCode).Updates(map[string]interface{}{
			"price": price,
		})
	}
}

// shutdown is called at application termination
func (a *App) shutdown(ctx context.Context) {
	defer PanicHandler()
	// Perform your teardown here
	//os.Exit(0)
	logger.SugaredLogger.Infof("application shutdown Version:%s", Version)
}

// Greet returns a greeting for the given name
func (a *App) Greet(stockCode string) *data.StockInfo {
	//stockInfo, _ := data.NewStockDataApi().GetStockCodeRealTimeData(stockCode)

	follow := &data.FollowedStock{
		StockCode: stockCode,
	}
	db.Dao.Model(follow).Where("stock_code = ?", stockCode).Preload("Groups").Preload("Groups.GroupInfo").First(follow)
	stockInfo := getStockInfo(*follow)
	return stockInfo
}

func (a *App) Follow(stockCode string) string {
	return data.NewStockDataApi().Follow(stockCode)
}

func (a *App) UnFollow(stockCode string) string {
	return data.NewStockDataApi().UnFollow(stockCode)
}

func (a *App) GetFollowList(groupId int) *[]data.FollowedStock {
	return data.NewStockDataApi().GetFollowList(groupId)
}

func (a *App) GetStockList(key string) []data.StockBasic {
	return data.NewStockDataApi().GetStockList(key)
}

func (a *App) SetCostPriceAndVolume(stockCode string, price float64, volume int64) string {
	return data.NewStockDataApi().SetCostPriceAndVolume(price, volume, stockCode)
}

func (a *App) SetAlarmChangePercent(val, alarmPrice float64, stockCode string) string {
	return data.NewStockDataApi().SetAlarmChangePercent(val, alarmPrice, stockCode)
}
func (a *App) SetStockSort(sort int64, stockCode string) {
	data.NewStockDataApi().SetStockSort(sort, stockCode)
}
func (a *App) SendDingDingMessage(message string, stockCode string) string {
	ttl, _ := a.cache.TTL([]byte(stockCode))
	logger.SugaredLogger.Infof("stockCode %s ttl:%d", stockCode, ttl)
	if ttl > 0 {
		return ""
	}
	err := a.cache.Set([]byte(stockCode), []byte("1"), 60*5)
	if err != nil {
		logger.SugaredLogger.Errorf("set cache error:%s", err.Error())
		return ""
	}
	return data.NewDingDingAPI().SendDingDingMessage(message)
}

// SendDingDingMessageByType msgType 报警类型: 1 涨跌报警;2 股价报警 3 成本价报警
func (a *App) SendDingDingMessageByType(message string, stockCode string, msgType int) string {

	if strutil.HasPrefixAny(stockCode, []string{"SZ", "SH", "sh", "sz"}) && (!isTradingTime(time.Now())) {
		return "非A股交易时间"
	}
	if strutil.HasPrefixAny(stockCode, []string{"hk", "HK"}) && (!IsHKTradingTime(time.Now())) {
		return "非港股交易时间"
	}
	if strutil.HasPrefixAny(stockCode, []string{"us", "US", "gb_"}) && (!IsUSTradingTime(time.Now())) {
		return "非美股交易时间"
	}

	ttl, _ := a.cache.TTL([]byte(stockCode))
	//logger.SugaredLogger.Infof("stockCode %s ttl:%d", stockCode, ttl)
	if ttl > 0 {
		return ""
	}
	err := a.cache.Set([]byte(stockCode), []byte("1"), getMsgTypeTTL(msgType))
	if err != nil {
		logger.SugaredLogger.Errorf("set cache error:%s", err.Error())
		return ""
	}
	stockInfo := &data.StockInfo{}
	db.Dao.Model(stockInfo).Where("code = ?", stockCode).First(stockInfo)
	go data.NewAlertWindowsApi("go-stock消息通知", getMsgTypeName(msgType), GenNotificationMsg(stockInfo), "").SendNotification()
	return data.NewDingDingAPI().SendDingDingMessage(message)
}

func (a *App) NewChatStream(stock, stockCode, question string, aiConfigId int, sysPromptId *int, enableTools bool) {
	config := data.GetSettingConfig()
	aiConfig, find := slice.FindBy(config.AiConfigs, func(i int, item *data.AIConfig) bool {
		return item.ID == uint(aiConfigId)
	})

	if !find {
		runtime.EventsEmit(a.ctx, "newChatStream", map[string]any{"code": 0, "content": "AI config not found"})
		runtime.EventsEmit(a.ctx, "newChatStream", "DONE")
		return
	}

	var msgs <-chan map[string]any
	if aiConfig.ApiType == "gemini" {
		// For Gemini, tools are not supported in the same way. We ignore `enableTools`.
		msgs = data.NewGeminiApi(a.ctx, aiConfigId).NewGeminiChatStream(stock, stockCode, question, sysPromptId)
	} else { // Default to openai
		if enableTools {
			msgs = data.NewDeepSeekOpenAi(a.ctx, aiConfigId).NewChatStream(stock, stockCode, question, sysPromptId, a.AiTools)
		} else {
			msgs = data.NewDeepSeekOpenAi(a.ctx, aiConfigId).NewChatStream(stock, stockCode, question, sysPromptId, []data.Tool{})
		}
	}

	for msg := range msgs {
		runtime.EventsEmit(a.ctx, "newChatStream", msg)
	}
	runtime.EventsEmit(a.ctx, "newChatStream", "DONE")
}

// createStreamHandler creates a channel to handle streaming responses and forward them to the frontend.
func (a *App) createStreamHandler(eventName string) chan map[string]any {
	ch := make(chan map[string]any)
	go func() {
		for msg := range ch {
			runtime.EventsEmit(a.ctx, eventName, msg)
		}
	}()
	return ch
}

func (a *App) SaveAIResponseResult(stockCode, stockName, result, chatId, question string, aiConfigId int) {
	config := data.GetSettingConfig()
	aiConfig, find := slice.FindBy(config.AiConfigs, func(i int, item *data.AIConfig) bool {
		return item.ID == uint(aiConfigId)
	})

	if !find {
		// Handle error: config not found
		return
	}

	if aiConfig.ApiType == "gemini" {
		data.NewGeminiApi(a.ctx, aiConfigId).SaveAIResponseResult(stockCode, stockName, result, chatId, question)
	} else {
		data.NewDeepSeekOpenAi(a.ctx, aiConfigId).SaveAIResponseResult(stockCode, stockName, result, chatId, question)
	}
}
func (a *App) GetAIResponseResult(stock string) *models.AIResponseResult {
	return data.NewDeepSeekOpenAi(a.ctx, 0).GetAIResponseResult(stock)
}

func (a *App) GetVersionInfo() *models.VersionInfo {
	return &models.VersionInfo{
		Version:           Version,
		Icon:              GetImageBase(icon),
		Alipay:            GetImageBase(alipay),
		Wxpay:             GetImageBase(wxpay),
		Wxgzh:             GetImageBase(wxgzh),
		Content:           VersionCommit,
		OfficialStatement: OFFICIAL_STATEMENT,
	}
}

//// checkChromeOnWindows 在 Windows 系统上检查谷歌浏览器是否安装
//func checkChromeOnWindows() bool {
//	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
//	if err != nil {
//		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
//		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\chrome.exe`, registry.QUERY_VALUE)
//		if err != nil {
//			return false
//		}
//		defer key.Close()
//	}
//	defer key.Close()
//	_, _, err = key.GetValue("Path", nil)
//	return err == nil
//}
//
//// checkEdgeOnWindows 在 Windows 系统上检查Edge浏览器是否安装，并返回安装路径
//func checkEdgeOnWindows() (string, bool) {
//	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
//	if err != nil {
//		// 尝试在 WOW6432Node 中查找（适用于 64 位系统上的 32 位程序）
//		key, err = registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`, registry.QUERY_VALUE)
//		if err != nil {
//			return "", false
//		}
//		defer key.Close()
//	}
//	defer key.Close()
//	path, _, err := key.GetStringValue("Path")
//	if err != nil {
//		return "", false
//	}
//	return path, true
//}

func GetImageBase(bytes []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(bytes)
}

func GenNotificationMsg(stockInfo *data.StockInfo) string {
	Price, err := convertor.ToFloat(stockInfo.Price)
	if err != nil {
		Price = 0
	}
	PreClose, err := convertor.ToFloat(stockInfo.PreClose)
	if err != nil {
		PreClose = 0
	}
	var RF float64
	if PreClose > 0 {
		RF = mathutil.RoundToFloat(((Price-PreClose)/PreClose)*100, 2)
	}

	return "[" + stockInfo.Name + "] " + stockInfo.Price + " " + convertor.ToString(RF) + "% " + stockInfo.Date + " " + stockInfo.Time
}

// msgType : 1 涨跌报警(5分钟);2 股价报警(30分钟) 3 成本价报警(30分钟)
func getMsgTypeTTL(msgType int) int {
	switch msgType {
	case 1:
		return 60 * 5
	case 2:
		return 60 * 30
	case 3:
		return 60 * 30
	default:
		return 60 * 5
	}
}

func getMsgTypeName(msgType int) string {
	switch msgType {
	case 1:
		return "涨跌报警"
	case 2:
		return "股价报警"
	case 3:
		return "成本价报警"
	default:
		return "未知类型"
	}
}

func onExit(a *App) {
	// 清理操作
	logger.SugaredLogger.Infof("systray onExit")
	//systray.Quit()
	//runtime.Quit(a.ctx)
}

func (a *App) UpdateConfig(settingConfig *data.SettingConfig) string {
	s1, _ := json.Marshal(settingConfig)
	logger.SugaredLogger.Infof("UpdateConfig:%s", s1)
	if settingConfig.RefreshInterval > 0 {
		if entryID, exists := a.cronEntrys["MonitorStockPrices"]; exists {
			a.cron.Remove(entryID)
		}
		id, _ := a.cron.AddFunc(fmt.Sprintf("@every %ds", settingConfig.RefreshInterval), func() {
			//logger.SugaredLogger.Infof("MonitorStockPrices:%s", time.Now())
			MonitorStockPrices(a)
		})
		a.cronEntrys["MonitorStockPrices"] = id
	}

	return data.UpdateConfig(settingConfig)
}

func (a *App) GetConfig() *data.SettingConfig {
	return data.GetSettingConfig()
}

func (a *App) ExportConfig() string {
	config := data.NewSettingsApi().Export()
	file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:                "导出配置文件",
		CanCreateDirectories: true,
		DefaultFilename:      "config.json",
	})
	if err != nil {
		logger.SugaredLogger.Errorf("导出配置文件失败:%s", err.Error())
		return err.Error()
	}
	err = os.WriteFile(file, []byte(config), os.ModePerm)
	if err != nil {
		logger.SugaredLogger.Errorf("导出配置文件失败:%s", err.Error())
		return err.Error()
	}
	return "导出成功:" + file
}

func (a *App) ShareAnalysis(stockCode, stockName string) string {
	//http://go-stock.sparkmemory.top:16688/upload
	res := data.NewDeepSeekOpenAi(a.ctx, 0).GetAIResponseResult(stockCode)
	if res != nil && len(res.Content) > 100 {
		analysisTime := res.CreatedAt.Format("2006/01/02")
		logger.SugaredLogger.Infof("%s analysisTime:%s", res.CreatedAt, analysisTime)
		response, err := resty.New().SetHeader("ua-x", "go-stock").R().SetFormData(map[string]string{
			"text":         res.Content,
			"stockCode":    stockCode,
			"stockName":    stockName,
			"analysisTime": analysisTime,
		}).Post("http://go-stock.sparkmemory.top:16688/upload")
		if err != nil {
			return err.Error()
		}
		return response.String()
	} else {
		return "分析结果异常"
	}
}

func (a *App) GetfundList(key string) []data.FundBasic {
	return data.NewFundApi().GetFundList(key)
}
func (a *App) GetFollowedFund() []data.FollowedFund {
	return data.NewFundApi().GetFollowedFund()
}
func (a *App) FollowFund(fundCode string) string {
	return data.NewFundApi().FollowFund(fundCode)
}
func (a *App) UnFollowFund(fundCode string) string {
	return data.NewFundApi().UnFollowFund(fundCode)
}
func (a *App) SaveAsMarkdown(stockCode, stockName string) string {
	res := data.NewDeepSeekOpenAi(a.ctx, 0).GetAIResponseResult(stockCode)
	if res != nil && len(res.Content) > 100 {
		analysisTime := res.CreatedAt.Format("2006-01-02_15_04_05")
		file, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
			Title:           "保存为Markdown",
			DefaultFilename: fmt.Sprintf("%s[%s]AI分析结果_%s.md", stockName, stockCode, analysisTime),
			Filters: []runtime.FileFilter{
				{
					DisplayName: "Markdown",
					Pattern:     "*.md;*.markdown",
				},
			},
		})
		if err != nil {
			return err.Error()
		}
		err = os.WriteFile(file, []byte(res.Content), 0644)
		return "已保存至：" + file
	}
	return "分析结果异常,无法保存。"
}

func (a *App) GetPromptTemplates(name, promptType string) *[]models.PromptTemplate {
	return data.NewPromptTemplateApi().GetPromptTemplates(name, promptType)
}
func (a *App) AddPrompt(prompt models.Prompt) string {
	promptTemplate := models.PromptTemplate{
		ID:      prompt.ID,
		Content: prompt.Content,
		Name:    prompt.Name,
		Type:    prompt.Type,
	}
	return data.NewPromptTemplateApi().AddPrompt(promptTemplate)
}
func (a *App) DelPrompt(id uint) string {
	return data.NewPromptTemplateApi().DelPrompt(id)
}
func (a *App) SetStockAICron(cronText, stockCode string) {
	data.NewStockDataApi().SetStockAICron(cronText, stockCode)
	if strutil.HasPrefixAny(stockCode, []string{"gb_"}) {
		stockCode = strings.ToUpper(stockCode)
		stockCode = strings.Replace(stockCode, "gb_", "us", 1)
		stockCode = strings.Replace(stockCode, "GB_", "us", 1)
	}
	if entryID, exists := a.cronEntrys[stockCode]; exists {
		a.cron.Remove(entryID)
	}
	follow := data.NewStockDataApi().GetFollowedStockByStockCode(stockCode)
	id, _ := a.cron.AddFunc(cronText, a.AddCronTask(follow))
	a.cronEntrys[stockCode] = id

}
func (a *App) AddGroup(group data.Group) string {
	ok := data.NewStockGroupApi(db.Dao).AddGroup(group)
	if ok {
		return "添加成功"
	} else {
		return "添加失败"
	}
}
func (a *App) GetGroupList() []data.Group {
	return data.NewStockGroupApi(db.Dao).GetGroupList()
}

func (a *App) UpdateGroupSort(id int, newSort int) bool {
	return data.NewStockGroupApi(db.Dao).UpdateGroupSort(id, newSort)
}

func (a *App) InitializeGroupSort() bool {
	return data.NewStockGroupApi(db.Dao).InitializeGroupSort()
}

func (a *App) GetGroupStockList(groupId int) []data.GroupStock {
	return data.NewStockGroupApi(db.Dao).GetGroupStockByGroupId(groupId)
}

func (a *App) AddStockGroup(groupId int, stockCode string) string {
	ok := data.NewStockGroupApi(db.Dao).AddStockGroup(groupId, stockCode)
	if ok {
		return "添加成功"
	} else {
		return "添加失败"
	}
}

func (a *App) RemoveStockGroup(code, name string, groupId int) string {
	ok := data.NewStockGroupApi(db.Dao).RemoveStockGroup(code, name, groupId)
	if ok {
		return "移除成功"
	} else {
		return "移除失败"
	}
}

func (a *App) RemoveGroup(groupId int) string {
	ok := data.NewStockGroupApi(db.Dao).RemoveGroup(groupId)
	if ok {
		return "移除成功"
	} else {
		return "移除失败"
	}
}

func (a *App) GetStockKLine(stockCode, stockName string, days int64) *[]data.KLineData {
	return data.NewStockDataApi().GetHK_KLineData(stockCode, "day", days)
}

func (a *App) GetStockMinutePriceLineData(stockCode, stockName string) map[string]any {
	res := make(map[string]any, 4)
	priceData, date := data.NewStockDataApi().GetStockMinutePriceData(stockCode)
	res["priceData"] = priceData
	res["date"] = date
	res["stockName"] = stockName
	res["stockCode"] = stockCode
	return res
}

func (a *App) GetStockCommonKLine(stockCode, stockName string, days int64) *[]data.KLineData {
	return data.NewStockDataApi().GetCommonKLineData(stockCode, "day", days)
}

func (a *App) GetTelegraphList(source string) *[]*models.Telegraph {
	telegraphs := data.NewMarketNewsApi().GetTelegraphList(source)
	return telegraphs
}

func (a *App) ReFleshTelegraphList(source string) *[]*models.Telegraph {
	data.NewMarketNewsApi().GetNewTelegraph(30)
	data.NewMarketNewsApi().GetSinaNews(30)
	telegraphs := data.NewMarketNewsApi().GetTelegraphList(source)
	return telegraphs
}

func (a *App) GlobalStockIndexes() map[string]any {
	return data.NewMarketNewsApi().GlobalStockIndexes(30)
}

func (a *App) SummaryStockNews(question string, aiConfigId int, sysPromptId *int, enableTools bool) {
	config := data.GetSettingConfig()
	aiConfig, find := slice.FindBy(config.AiConfigs, func(i int, item *data.AIConfig) bool {
		return item.ID == uint(aiConfigId)
	})

	if !find {
		runtime.EventsEmit(a.ctx, "summaryStockNews", map[string]any{"code": 0, "content": "AI config not found"})
		runtime.EventsEmit(a.ctx, "summaryStockNews", "DONE")
		return
	}

	var msgs <-chan map[string]any
	if aiConfig.ApiType == "gemini" {
		// For Gemini, tools are not supported in the same way. We ignore `enableTools`.
		msgs = data.NewGeminiApi(a.ctx, aiConfigId).NewGeminiSummaryStockNewsStream(question, sysPromptId)
	} else { // Default to openai
		if enableTools {
			msgs = data.NewDeepSeekOpenAi(a.ctx, aiConfigId).NewSummaryStockNewsStreamWithTools(question, sysPromptId, a.AiTools)
		} else {
			msgs = data.NewDeepSeekOpenAi(a.ctx, aiConfigId).NewSummaryStockNewsStream(question, sysPromptId)
		}
	}

	for msg := range msgs {
		runtime.EventsEmit(a.ctx, "summaryStockNews", msg)
	}
	runtime.EventsEmit(a.ctx, "summaryStockNews", "DONE")
}
func (a *App) GetIndustryRank(sort string, cnt int) []any {
	res := data.NewMarketNewsApi().GetIndustryRank(sort, cnt)
	return res["data"].([]any)
}
func (a *App) GetIndustryMoneyRankSina(fenlei, sort string) []map[string]any {
	res := data.NewMarketNewsApi().GetIndustryMoneyRankSina(fenlei, sort)
	return res
}
func (a *App) GetMoneyRankSina(sort string) []map[string]any {
	res := data.NewMarketNewsApi().GetMoneyRankSina(sort)
	return res
}

func (a *App) GetStockMoneyTrendByDay(stockCode string, days int) []map[string]any {
	res := data.NewMarketNewsApi().GetStockMoneyTrendByDay(stockCode, days)
	slice.Reverse(res)
	return res
}

// OpenURL
//
//	@Description:  跨平台打开默认浏览器
//	@receiver a
//	@param url
func (a *App) OpenURL(url string) {
	runtime.BrowserOpenURL(a.ctx, url)
}

// SaveImage
//
//	@Description: 跨平台保存图片
//	@receiver a
//	@param name
//	@param base64Data
//	@return error
func (a *App) SaveImage(name, base64Data string) string {
	// 打开保存文件对话框
	filePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存图片",
		DefaultFilename: name + "AI分析.png",
		Filters: []runtime.FileFilter{
			{
				DisplayName: "PNG 图片",
				Pattern:     "*.png",
			},
		},
	})
	if err != nil || filePath == "" {
		return "文件路径,无法保存。"
	}

	// 解码并保存
	decodeString, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "文件内容异常,无法保存。"
	}

	err = os.WriteFile(filepath.Clean(filePath), decodeString, os.ModePerm)
	if err != nil {
		return "保存结果异常,无法保存。"
	}
	return filePath
}

// SaveWordFile
//
//	@Description: // 跨平台保存word
//	@receiver a
//	@param filename
//	@param base64Data
//	@return error
func (a *App) SaveWordFile(filename string, base64Data string) string {
	// 弹出保存文件对话框
	filePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "保存 Word 文件",
		DefaultFilename: filename,
		Filters: []runtime.FileFilter{
			{DisplayName: "Word 文件", Pattern: "*.docx"},
		},
	})
	if err != nil || filePath == "" {
		return "文件路径,无法保存。"
	}

	// 解码 base64 内容
	decodeString, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "文件内容异常,无法保存。"
	}
	// 保存为文件
	err = os.WriteFile(filepath.Clean(filePath), decodeString, 0777)
	if err != nil {
		return "保存结果异常,无法保存。"
	}
	return filePath
}

// GetAiConfigs
//
//	@Description: // 获取AiConfig列表
//	@receiver a
//	@return error
func (a *App) GetAiConfigs() []*data.AIConfig {
	return data.GetSettingConfig().AiConfigs
}

func (a *App) StartAIStockScreenerStream(aiConfigId int) {
	go func() {
		// Node 1: Data Gathering
		logger.SugaredLogger.Infof("AI Stock Screener: Starting Data Gathering")
		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":   "data_gathering",
			"status": "processing",
		})

		var newsText strings.Builder
		// Market News
		news := data.NewMarketNewsApi().GetNewsList("", 100)
		for _, telegraph := range *news {
			newsText.WriteString(fmt.Sprintf("## %s:\n### %s\n", telegraph.Time, telegraph.Content))
		}

		// Global Stock Indexes
		indexes := data.NewMarketNewsApi().GlobalStockIndexes(30)
		indexesText, _ := json.Marshal(indexes)
		newsText.WriteString("\n\n## Global Stock Indexes:\n")
		newsText.WriteString(string(indexesText))

		// Industry Rank
		industryRank := data.NewMarketNewsApi().GetIndustryRank("zdf", 30)
		industryRankText, _ := json.Marshal(industryRank)
		newsText.WriteString("\n\n## Industry Rank:\n")
		newsText.WriteString(string(industryRankText))

		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":    "data_gathering",
			"status":  "complete",
			"payload": "数据收集完成",
		})
		logger.SugaredLogger.Infof("AI Stock Screener: Data Gathering Complete")

		// Node 2: Event Analysis
		logger.SugaredLogger.Infof("AI Stock Screener: Starting Event Analysis")
		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":   "event_analysis",
			"status": "processing",
		})

		geminiAPI := data.NewGeminiApi(a.ctx, aiConfigId)
		prompt1 := `
			你是一个经验丰富的股票市场分析师. 请根据以下最新的市场新闻、全球股指信息和行业排名数据, 分析并识别出未来几周最有潜力的3-5个行业板块.
			请说明你选择这些板块的理由, 并为每个板块挑选出1-2只龙头股.

			在完成板块和龙头股分析后，请综合所有信息，从你选出的所有龙头股中，最终筛选出**三支**最具投资潜力的股票。

			你需要考虑的因素包括:
			- 宏观经济趋势
			- 近期重大新闻事件的影响
			- 行业整体表现和资金流向
			- 市场情绪

			请以Markdown格式返回你的分析结果, 结果应该清晰、简洁, 重点突出.
			最后，请将最终筛选出的**三支**股票代码以'@@STOCKS:'为前缀单独在一行输出，并以英文逗号分隔，例如：@@STOCKS:sh600036,sz000001,hk00700

			以下是原始数据:
		 ` + newsText.String()
		var eventAnalysisResult strings.Builder
		eventAnalysisCh := geminiAPI.StreamGenerateContent(prompt1)
		for msg := range eventAnalysisCh {
			if content, ok := msg["content"].(string); ok {
				eventAnalysisResult.WriteString(content)
				runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
					"node":    "event_analysis",
					"status":  "streaming",
					"payload": content,
				})
			}
		}

		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":   "event_analysis",
			"status": "complete",
		})
		logger.SugaredLogger.Infof("AI Stock Screener: Event Analysis Complete")

		// Node 3: Technical Analysis
		logger.SugaredLogger.Infof("AI Stock Screener: Starting Technical Analysis")
		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":   "technical_analysis",
			"status": "processing",
		})

		// Parse stocks from the previous step
		var stocksToAnalyze []string
		lines := strings.Split(eventAnalysisResult.String(), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "@@STOCKS:") {
				stocksStr := strings.TrimPrefix(line, "@@STOCKS:")
				stocksToAnalyze = strings.Split(stocksStr, ",")
				break
			}
		}
		logger.SugaredLogger.Infof("AI Stock Screener: Found stocks to analyze: %v", stocksToAnalyze)

		var techAnalysisResult strings.Builder
		for _, stockCode := range stocksToAnalyze {
			stockCode = strings.TrimSpace(stockCode)
			if stockCode == "" {
				continue
			}
			stockName := data.NewStockDataApi().GetStockNameByCode(stockCode)
			logger.SugaredLogger.Infof("AI Stock Screener: Analyzing %s (%s)", stockName, stockCode)

			klineData := data.NewStockDataApi().GetCommonKLineData(stockCode, "day", 120)
			klineText, _ := json.Marshal(klineData)

			prompt2 := `
				你是一位精通各种技术指标的股票技术分析专家. 请对以下这只股票进行深入的技术分析.

				股票名称: ` + stockName + `
				股票代码: ` + stockCode + `

				请综合运用以下技术指标进行分析:
				- **移动平均线 (MA)**: 分析短期、中期、长期均线的排列情况(如金叉、死叉、多头排列、空头排列).
				- **相对强弱指数 (RSI)**: 判断股票的超买超卖状态.
				- **MACD 指标**: 分析DIF和DEA线的走势, 以及红绿柱状图的变化.
				- **布林带 (BOLL)**: 分析股价与上、中、下轨的关系.
				- **成交量 (Volume)**: 结合股价走势, 分析量价关系.

				请根据你的分析, 给出该股票未来的走势预测, 并明确指出关键的支撑位和阻力位.

				以下是该股票最近120天的日K线数据:
			` + string(klineText)

			techAnalysisCh := geminiAPI.StreamGenerateContent(prompt2)
			for msg := range techAnalysisCh {
				if content, ok := msg["content"].(string); ok {
					techAnalysisResult.WriteString(content)
					runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
						"node":    "technical_analysis",
						"status":  "streaming",
						"payload": content,
					})
				}
			}
			techAnalysisResult.WriteString("\n\n---\n\n")
		}

		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":   "technical_analysis",
			"status": "complete",
		})
		logger.SugaredLogger.Infof("AI Stock Screener: Technical Analysis Complete")

		// Node 4: Final Report
		logger.SugaredLogger.Infof("AI Stock Screener: Starting Final Report")
		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":   "final_report",
			"status": "processing",
		})

		prompt3 := `
			你是一位顶级的投资策略师. 请综合以下的市场板块分析和个股技术分析结果, 为投资者制定一份清晰、可执行的投资策略.

			请**直接**在报告的开头部分，使用以下格式，明确给出最终的投资建议：

			---
			**核心投资建议**

			*   **股票代码**: [股票代码1], [股票代码2], ...
			*   **建议买入点位**:
				*   [股票代码1]: [具体价格区间]
				*   [股票代码2]: [具体价格区间]
				*   ...
			*   **仓位分配**:
				*   [股票代码1]: [百分比]
				*   [股票代码2]: [百分比]
				*   ...
		---

			然后，在报告的主体部分，请包含以下内容：
			1.  **投资组合建议**: 详细说明为什么选择这几只股票构成投资组合.
			2.  **投资逻辑**: 详细阐述推荐这个投资组合的核心逻辑，结合宏观、行业和技术面.
			3.  **风险提示**: 指出这个投资组合可能面临的主要风险.
			4.  **总结**: 对整个投资策略进行总结.

			请确保你的报告逻辑清晰, 语言专业, 并以易于理解的Markdown格式呈现.

			以下是分析资料:

			**市场板块分析:**
			` + eventAnalysisResult.String() + `

			**个股技术分析:**
			` + techAnalysisResult.String() + `
		`

		finalReportCh := geminiAPI.StreamGenerateContent(prompt3)
		for msg := range finalReportCh {
			if content, ok := msg["content"].(string); ok {
				runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
					"node":    "final_report",
					"status":  "streaming",
					"payload": content,
				})
			}
		}

		runtime.EventsEmit(a.ctx, "ai_screener_update", map[string]any{
			"node":   "final_report",
			"status": "complete",
		})
		logger.SugaredLogger.Infof("AI Stock Screener: Final Report Complete")
	}()
}
