package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/gabriel-vasile/mimetype"
	"github.com/valyala/fasthttp"
	"golang.org/x/net/html/charset"
	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"
)

// Config 定义配置文件结构体
type Config struct {
	Account []struct {
		Name   string `yaml:"name"`
		Cookie string `yaml:"cookie"`
	} `yaml:"account"`
	Push struct {
		BotToken string `yaml:"bot_token"`
		ChatID   string `yaml:"chat_id"`
	} `yaml:"push"`
}

// httpClient 定义全局 HTTP 客户端 (使用 fasthttp)
var httpClient = &fasthttp.Client{
	MaxConnsPerHost:     200,
	MaxIdleConnDuration: 30 * time.Second,
	MaxConnDuration:     5 * time.Minute,
}

// 存储每个账户的 formhash，key 为 cookie，value 为 formhash
var formhashCache sync.Map

// accountPostCache 定义每个账户的帖子缓存
var accountPostCache sync.Map // map[string]*sync.Map

// loadConfig 从配置文件加载配置
func loadConfig(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// sendRequest 发送 HTTP 请求
func sendRequest(method, url string, body string, headers map[string]string, cookie string) ([]byte, error) {
	req := fasthttp.AcquireRequest()
	resp := fasthttp.AcquireResponse()
	defer fasthttp.ReleaseRequest(req)
	defer fasthttp.ReleaseResponse(resp)

	req.SetRequestURI(url)
	req.Header.SetMethod(method)

	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if method == "POST" {
		req.SetBodyString(body)
	}

	err := httpClient.Do(req, resp)
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %w", err)
	}

	return resp.Body(), nil
}

// tsdmCheckIn 执行天使动漫论坛签到
func tsdmCheckIn(cookie string) (string, error) {
	var formhash string
	var ok bool
	var retryCount int

	// 尝试从缓存中获取 formhash
	if cachedFormhash, loaded := formhashCache.Load(cookie); loaded {
		if formhash, ok = cachedFormhash.(string); ok {
			// 使用缓存的 formhash 进行签到操作，最多重试 3 次
			for retryCount < 3 {
				result, err := doCheckIn(cookie, formhash)
				if err == nil {
					return result, nil
				}
				retryCount++
				fmt.Println("签到失败，重试次数:", retryCount)
				time.Sleep(1 * time.Second) // 等待 1 秒后重试
			}
			// 重试 3 次后仍然失败，重新获取 formhash
			fmt.Println("签到失败，重新获取 formhash")
			formhashCache.Delete(cookie) // 删除缓存的 formhash
		}
	}

	// 如果缓存中没有 formhash 或 formhash 过期，则发送请求获取
	respData, err := sendRequest("GET", "https://www.tsdm39.com/forum.php", "", nil, cookie)
	if err != nil {
		return "", fmt.Errorf("获取页面内容失败: %w", err)
	}

	// 使用 goquery 解析 HTML 代码
	contentType := mimetype.Detect(respData).String()
	reader, err := charset.NewReader(strings.NewReader(string(respData)), contentType)
	if err != nil {
		return "", fmt.Errorf("创建 reader 失败: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}

	// 提取 formhash
	formhash, exists := doc.Find("input[name='formhash']").Attr("value")
	if !exists {
		return "", fmt.Errorf("formhash 不存在")
	}

	// 将 formhash 存储到缓存中，并设置过期时间
	formhashCache.Store(cookie, formhash)
	time.AfterFunc(30*24*time.Hour, func() {
		formhashCache.Delete(cookie)
	})

	// 使用新获取的 formhash 进行签到操作
	return doCheckIn(cookie, formhash)
}

// doCheckIn 使用指定的 formhash 执行签到操作
func doCheckIn(cookie, formhash string) (string, error) {
	// 签到
	formData := url.Values{
		"formhash":  {formhash},
		"qdxq":      {"kx"},
		"qdmode":    {"3"},
		"todaysay":  {""},
		"fastreply": {"1"},
	}

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       "https://www.tsdm39.com",
	}

	respData, err := sendRequest("POST", "https://www.tsdm39.com/plugin.php?id=dsu_paulsign%3Asign&operation=qiandao&infloat=1&sign_as=1&inajax=1", formData.Encode(), headers, cookie)
	if err != nil {
		return "", fmt.Errorf("签到请求失败: %w", err)
	}

	// 检查签到结果
	checkInSuccessRegex := regexp.MustCompile(`签到成功`)
	alreadyRegex := regexp.MustCompile(`您今日已经签到`)

	// 使用 goquery 解析 HTML 代码
	contentType := mimetype.Detect(respData).String()
	reader, err := charset.NewReader(strings.NewReader(string(respData)), contentType)
	if err != nil {
		return "", fmt.Errorf("创建 reader 失败: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}

	// 查找包含签到结果的 div 元素
	resultText := doc.Find(".c").First().Text()

	// 提取天使币奖励信息
	angelCoinRegex := regexp.MustCompile(`天使币 (\d+)`)
	angelCoinMatches := angelCoinRegex.FindAllStringSubmatch(resultText, -1)
	var totalAngelCoins int
	for _, match := range angelCoinMatches {
		coins, _ := strconv.Atoi(match[1])
		totalAngelCoins += coins
	}

	// 提取额外奖励信息
	extraRewardRegex := regexp.MustCompile(`额外奖励 天使币 (\d+)`)
	extraRewardMatch := extraRewardRegex.FindStringSubmatch(resultText)
	var extraReward int
	if len(extraRewardMatch) > 1 {
		extraReward, _ = strconv.Atoi(extraRewardMatch[1])
	}

	// 提取签到排名信息
	rankingRegex := regexp.MustCompile(`您是今天第(\d+)个签到的会员`)
	rankingMatch := rankingRegex.FindStringSubmatch(resultText)
	var ranking int
	if len(rankingMatch) > 1 {
		ranking, _ = strconv.Atoi(rankingMatch[1])
	} else {
		// 如果没有匹配到排名信息，则将排名设置为 -1
		ranking = -1
	}

	if checkInSuccessRegex.MatchString(resultText) {
		if extraReward > 0 {
			if ranking != -1 { // 如果有排名信息
				return fmt.Sprintf("签到成功，您是今天第 %d 个签到的会员，获得天使币 %d (包含额外奖励 %d)", ranking, totalAngelCoins, extraReward), nil
			} else { // 如果没有排名信息
				return fmt.Sprintf("签到成功，获得天使币 %d (包含额外奖励 %d)", totalAngelCoins, extraReward), nil
			}
		} else {
			if ranking != -1 { // 如果有排名信息
				return fmt.Sprintf("签到成功，您是今天第 %d 个签到的会员，获得天使币 %d", ranking, totalAngelCoins), nil
			} else { // 如果没有排名信息
				return fmt.Sprintf("签到成功，获得天使币 %d", totalAngelCoins), nil
			}
		}
	} else if alreadyRegex.MatchString(resultText) {
		return "您今天已经签到", nil
	} else {
		return "", fmt.Errorf("签到失败: %s", resultText)
	}
}

// tsdmWork 执行天使动漫论坛打工任务
func tsdmWork(accountName, cookie string) (bool, time.Duration, error) {
	headers := map[string]string{
		"User-Agent":       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		"Connection":       "Keep-Alive",
		"X-Requested-With": "XMLHttpRequest",
		"Referer":          "https://www.tsdm39.net/plugin.php?id=np_cliworkdz:work",
		"Content-Type":     "application/x-www-form-urlencoded",
	}

	// 检查是否可以打工
	data, err := sendRequest("GET", "https://www.tsdm39.com/plugin.php?id=np_cliworkdz%3Awork&inajax=1", "", headers, cookie)
	if err != nil {
		return false, 0, fmt.Errorf("检查打工状态失败: %w", err)
	}

	waitRegex := regexp.MustCompile(`您需要等待(\d+)小时(\d+)分钟(\d+)秒后即可进行。`)
	if waitRegex.MatchString(string(data)) {
		matches := waitRegex.FindStringSubmatch(string(data))
		hours, _ := strconv.Atoi(matches[1])
		minutes, _ := strconv.Atoi(matches[2])
		seconds, _ := strconv.Atoi(matches[3])
		waitDuration := time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second
		return false, waitDuration, nil // 返回计算出的等待时间
	}

	// 打工
	formData := url.Values{"act": {"clickad"}}
	ticker := time.NewTicker(3 * time.Second) // 使用 ticker 控制打工请求间隔
	defer ticker.Stop()
	for i := 0; i < 6; i++ {
		<-ticker.C // 等待 ticker 事件
		_, err := sendRequest("POST", "https://www.tsdm39.com/plugin.php?id=np_cliworkdz:work", formData.Encode(), headers, cookie)
		if err != nil {
			fmt.Printf("[%s] 打工请求失败: %v\n", accountName, err)
			return false, 0, fmt.Errorf("打工请求失败: %w", err)
		}
	}

	// 获取奖励
	formData = url.Values{"act": {"getcre"}}
	data, err = sendRequest("POST", "https://www.tsdm39.com/plugin.php?id=np_cliworkdz:work", formData.Encode(), headers, cookie)
	if err != nil {
		fmt.Printf("[%s] 获取奖励失败: %v\n", accountName, err)
		return false, 0, fmt.Errorf("获取奖励失败: %w", err)
	}

	// 检查是否打工成功
	workSuccessRegex := regexp.MustCompile(`恭喜，您已经成功领取了奖励天使币 \+\d+`)
	if workSuccessRegex.MatchString(string(data)) {
		fmt.Printf("[%s] 打工成功\n", accountName)

		return true, 6 * time.Hour, nil // 打工成功后，返回 6 小时的等待时间
	}

	fmt.Printf("[%s] 打工失败: %s\n", accountName, string(data))
	return false, 0, fmt.Errorf("打工失败: %s", string(data))
}

// getScore 获取用户天使币数量
func getScore(cookie string) (string, error) {
	respData, err := sendRequest("GET", "https://www.tsdm39.com/home.php?mod=spacecp&ac=credit&showcredit=1", "", nil, cookie)
	if err != nil {
		return "", fmt.Errorf("获取积分信息失败: %w", err)
	}

	// 使用 mimetype 检测内容类型
	contentType := mimetype.Detect(respData).String()
	reader, err := charset.NewReader(strings.NewReader(string(respData)), contentType)
	if err != nil {
		return "", fmt.Errorf("创建 reader 失败: %w", err)
	}

	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 失败: %w", err)
	}
	// 查找包含天使币数量的 li 元素
	angelCoins := doc.Find(".creditl .xi1").First().Text()
	angelCoins = strings.TrimSpace(strings.Replace(angelCoins, "天使币:", "", 1))

	return angelCoins, nil
}

// checkPosts 检查帖子列表并尝试抢红包
func checkPosts(accountName string, cookie string, botToken string, chatID string) {
	// 获取账户的缓存
	var postCache *sync.Map
	if cache, ok := accountPostCache.Load(accountName); ok {
		postCache = cache.(*sync.Map)
	} else {
		postCache = &sync.Map{}
		accountPostCache.Store(accountName, postCache)
	}

	// 获取帖子列表页面
	respData, err := sendRequest("GET", "https://www.tsdm39.com/forum.php?mod=forumdisplay&fid=4", "", nil, cookie)
	if err != nil {
		fmt.Println("获取帖子列表页面失败:", err)
		return
	}

	// 使用 goquery 解析 HTML 代码
	contentType := mimetype.Detect(respData).String()
	reader, err := charset.NewReader(strings.NewReader(string(respData)), contentType)
	if err != nil {
		fmt.Println("创建 reader 失败:", err)
		return
	}

	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		fmt.Println("解析 HTML 失败:", err)
		return
	}

	var wg sync.WaitGroup // 创建 WaitGroup

	// 查找帖子列表
	doc.Find("tbody[id^='normalthread_']").Each(func(i int, s *goquery.Selection) {
		// 提取帖子链接
		link, exists := s.Find("th a.xst").Attr("href")
		if !exists {
			fmt.Println("帖子链接不存在")
			return
		}

		wg.Add(1) // 为每个 goroutine 增加计数

		// 提取帖子 ID
		tidRegex := regexp.MustCompile(`tid=(\d+)`)
		tidMatches := tidRegex.FindStringSubmatch(link)
		if len(tidMatches) <= 1 {
			fmt.Println("帖子 ID 不存在")
			return
		}
		tid := tidMatches[1]

		// 使用 goroutine 并行处理抢红包任务
		go func(tid string) {
			defer wg.Done() // 在 goroutine 结束时减少计数
			// 检查缓存
			if cached, ok := postCache.Load(tid); ok {
				// 缓存存在，检查缓存创建时间是否超过 3 天
				if time.Since(cached.(time.Time)) > 3*24*time.Hour {
					// 缓存创建时间超过 3 天，刷新缓存时间，延长至 7 天
					postCache.Store(tid, time.Now())
					time.AfterFunc(7*24*time.Hour, func() {
						postCache.Delete(tid)
					})
					return
				} else {
					// 缓存创建时间未超过 3 天，不做任何操作
					return
				}
			}
			// 缓存不存在或已过期，尝试抢红包
			redPacketAngelCoins, redPacketResult, err := grabRedPacket(tid, cookie)
			if err != nil {
				// 不输出错误信息
			} else {
				// 如果抢到红包，推送消息
				if redPacketAngelCoins > 0 {
					push(fmt.Sprintf("[%s] %s", accountName, redPacketResult), botToken, chatID)
				}
			}

			// 更新缓存并设置过期时间为 7 天
			postCache.Store(tid, time.Now())
			time.AfterFunc(7*24*time.Hour, func() {
				postCache.Delete(tid)
			})
		}(tid)
	})
	wg.Wait() // 等待所有 goroutine 执行完毕
}

// grabRedPacket 尝试抢红包
func grabRedPacket(tid string, cookie string) (int, string, error) {
	redPacketURL := fmt.Sprintf("https://tsdm39.com/plugin.php?id=tsdmbet:awardPacket&action=getaward&tid=%s", tid)

	// 发送红包请求
	respData, err := sendRequest("GET", redPacketURL, "", nil, cookie)
	if err != nil {
		return 0, "", fmt.Errorf("红包请求失败: %w", err)
	}

	// 检查红包结果
	redPacketSuccessRegex := regexp.MustCompile(`领取红包 (\d+) 天使币`)
	redPacketFailRegex := regexp.MustCompile(`来晚了`)
	redPacketAlreadyRegex := regexp.MustCompile(`已经领取过这个主题的红包了`)
	redPacketNoRedPacketRegex := regexp.MustCompile(`这个主题并没有红包`)

	if redPacketSuccessRegex.MatchString(string(respData)) {
		matches := redPacketSuccessRegex.FindStringSubmatch(string(respData))
		redPacketAngelCoins, _ := strconv.Atoi(matches[1])
		return redPacketAngelCoins, fmt.Sprintf("抢到红包啦！获得 %d 天使币", redPacketAngelCoins), nil
	} else if redPacketFailRegex.MatchString(string(respData)) {
		return 0, "来晚了，红包已被抢光", nil
	} else if redPacketAlreadyRegex.MatchString(string(respData)) {
		return 0, "您已领取过此红包", nil
	} else if redPacketNoRedPacketRegex.MatchString(string(respData)) {
		return 0, "这个主题并没有红包", nil
	} else {
		return 0, "", fmt.Errorf("未知错误: %s", string(respData))
	}
}

// telegramPush 发送 Telegram 推送消息
func telegramPush(sendTitle, pushMessage, botToken, chatID string) error {
	formData := url.Values{
		"chat_id": {chatID},
		"text":    {sendTitle + "\r\n" + pushMessage},
	}

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
	}

	_, err := sendRequest("POST", "https://api.telegram.org/bot"+botToken+"/sendMessage", formData.Encode(), headers, "")
	if err != nil {
		return fmt.Errorf("telegram 推送失败: %w", err)
	}
	return nil
}

// push 发送推送消息
func push(data, botToken, chatID string) {
	go func() {
		err := telegramPush("【天使动漫论坛任务推送】", data, botToken, chatID)
		if err != nil {
			fmt.Println(err)
		}
	}()
}

// pushCheckInResult 推送签到结果
func pushCheckInResult(accountName, result, botToken, chatID string) {
	// 只在签到成功时推送消息
	checkInSuccessRegex := regexp.MustCompile(`签到成功`)
	if checkInSuccessRegex.MatchString(result) {
		push(fmt.Sprintf("[%s] 签到结果: %s", accountName, result), botToken, chatID)
	}
}

// pushWorkResult 推送打工结果
func pushWorkResult(accountName string, success bool, score, botToken, chatID string) {
	if success {
		push(fmt.Sprintf("[%s] 打工成功，已拥有天使币数量: %s", accountName, score), botToken, chatID)
	}
}

// runCheckIn 运行签到任务
func runCheckIn(accountName, cookie, botToken, chatID string) {
	checkInResult, err := tsdmCheckIn(cookie)
	if err != nil {
		fmt.Printf("[%s] 签到错误: %v\n", accountName, err)
	} else {
		fmt.Printf("[%s] %s\n", accountName, checkInResult)
		pushCheckInResult(accountName, checkInResult, botToken, chatID)
	}
}

// runWork 运行打工任务
func runWork(accountName, cookie, botToken, chatID string) time.Duration {
	workSuccess, waitDuration, err := tsdmWork(accountName, cookie)
	if err != nil {
		fmt.Printf("[%s] 打工错误: %v\n", accountName, err)
		//push(fmt.Sprintf("[%s] 打工失败: %v", accountName, err), botToken, chatID) // 推送打工失败信息
	} else {
		score, scoreErr := getScore(cookie)
		if scoreErr != nil {
			fmt.Printf("[%s] 获取天使币数量失败: %v\n", accountName, scoreErr)
		} else {
			fmt.Printf("[%s] 天使币数量: %s\n", accountName, score)
			if workSuccess { // 只在打工成功时推送打工成功信息
				pushWorkResult(accountName, workSuccess, score, botToken, chatID)
			}
		}

		if !workSuccess && waitDuration == 0 {
			waitDuration = 1 * time.Minute
		}

		fmt.Printf("[%s] 下次打工将在 %s 后进行\n", accountName, waitDuration)
	}
	return waitDuration
}

// run 运行程序
func run(config *Config, daemonMode bool) {
	if daemonMode {
		// 守护进程模式
		if os.Getppid() != 1 {
			// 创建子进程并退出父进程
			pid, err := syscall.ForkExec(os.Args[0], os.Args, &syscall.ProcAttr{
				Files: []uintptr{os.Stdin.Fd(), os.Stdout.Fd(), os.Stderr.Fd()},
			})
			if err != nil {
				fmt.Println("无法创建子进程:", err)
				return
			}
			fmt.Printf("后台进程已启动，PID: %d\n", pid)
			os.Exit(0)
		}

		//丢弃输出
		devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			fmt.Println("无法打开 /dev/null:", err)
			return
		}
		defer devNull.Close()

		os.Stdout = devNull
		os.Stderr = devNull

		// 捕获信号
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

		// 创建 context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 使用 errgroup 管理并发任务
		group, ctx := errgroup.WithContext(ctx)

		// 创建动态时区
		location, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			fmt.Println("无法加载时区:", err)
			return
		}

		for _, account := range config.Account {
			acc := account // 避免闭包陷阱

			// --- 签到任务 ---
			group.Go(func() error {
				// 在 -d 模式下，先执行一次签到任务
				checkInResult, err := tsdmCheckIn(acc.Cookie)
				if err == nil {
					fmt.Printf("[%s] %s\n", acc.Name, checkInResult)
					pushCheckInResult(acc.Name, checkInResult, config.Push.BotToken, config.Push.ChatID)
				} else {
					// 记录错误日志
					fmt.Printf("[%s] 签到失败: %v\n", acc.Name, err)
				}

				// 计算下一次运行时间（UTC+8），提前10秒
				now := time.Now().In(location)
				nextRun := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location).Add(-10 * time.Second)
				if now.After(nextRun) {
					nextRun = nextRun.AddDate(0, 0, 1)
				}

				ticker := time.NewTicker(time.Until(nextRun))

				defer ticker.Stop()

				maxRetryTimes := 100              // 最大重试次数
				retryInterval := 15 * time.Minute // 重试间隔

				for {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-ticker.C:
						// 创建一个可取消的 context
						childCtx, cancel := context.WithCancel(ctx)
						defer cancel() // 确保 context 最终被取消

						// 并发尝试签到
						resultChan := make(chan string, 1) // 并发1次
						errChan := make(chan error, 11)

						for i := 0; i < 11; i++ {
							go func() {
								select {
								case <-childCtx.Done():
									return // context 已被取消，停止执行
								default:
									// 添加固定延迟
									time.Sleep(time.Duration(i) * 1000 * time.Millisecond) // 每个 goroutine 延迟递增 1 秒
									checkInResult, err := tsdmCheckIn(acc.Cookie)
									if err != nil {
										errChan <- err
									} else {
										// 只有签到成功才发送到 resultChan
										if strings.Contains(checkInResult, "签到成功") {
											resultChan <- checkInResult
										}
									}
								}
							}()
						}

						select {
						case checkInResult := <-resultChan:
							fmt.Printf("[%s] 签到成功: %s\n", acc.Name, checkInResult)
							pushCheckInResult(acc.Name, checkInResult, config.Push.BotToken, config.Push.ChatID)
							cancel() // 签到成功，取消 context，通知其他 goroutine 停止执行
							return nil

						case err := <-errChan:
							fmt.Printf("[%s] 签到错误: %v\n", acc.Name, err)
							// 签到失败，进行重试
							for i := 0; i < maxRetryTimes; i++ {
								fmt.Printf("[%s] 开始第 %d 次重试...\n", acc.Name, i+1)
								checkInResult, err := tsdmCheckIn(acc.Cookie)
								if err != nil {
									fmt.Printf("[%s] 重试签到错误: %v\n", acc.Name, err)
									if i < maxRetryTimes-1 {
										time.Sleep(retryInterval)
									}
								} else {
									fmt.Printf("[%s] 重试签到成功: %s\n", acc.Name, checkInResult)
									pushCheckInResult(acc.Name, checkInResult, config.Push.BotToken, config.Push.ChatID)
									break // 签到成功，退出重试循环
								}
							}
						}
					}
				}
			})

			// --- 打工任务 ---
			group.Go(func() error {
				ticker := time.NewTicker(1 * time.Minute) // 设置初始的 ticker 间隔为 1 分钟
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return nil // 退出循环
					case <-ticker.C:
						waitDuration := runWork(acc.Name, acc.Cookie, config.Push.BotToken, config.Push.ChatID)
						if waitDuration == 0 {
							waitDuration = 1 * time.Minute // 设置最小等待时间
						}
						ticker.Reset(waitDuration) // 重置 ticker 的间隔时间
					}
				}
			})

			// --- 抢红包任务 ---
			group.Go(func() error {
				ticker := time.NewTicker(5 * time.Minute)
				defer ticker.Stop()

				for {
					select {
					case <-ctx.Done():
						return nil
					case <-ticker.C:
						checkPosts(acc.Name, acc.Cookie, config.Push.BotToken, config.Push.ChatID)
					}
				}
			})

		}

		// 等待信号并取消 context
		go func() {
			<-sigs
			cancel()
		}()

		// 等待所有任务完成
		if err := group.Wait(); err != nil && err != context.Canceled {
			fmt.Println("并发任务出错:", err)
		}

	} else {
		// 非守护进程模式
		for _, account := range config.Account {
			runCheckIn(account.Name, account.Cookie, config.Push.BotToken, config.Push.ChatID)
			runWork(account.Name, account.Cookie, config.Push.BotToken, config.Push.ChatID)
			checkPosts(account.Name, account.Cookie, config.Push.BotToken, config.Push.ChatID)
		}
	}
}

func main() {
	configPath := flag.String("c", "config.yaml", "配置文件路径")
	daemonMode := flag.Bool("d", false, "是否以后台守护进程方式运行")
	flag.Parse()

	config, err := loadConfig(*configPath)
	if err != nil {
		fmt.Println("加载配置文件失败:", err)
		return
	}

	run(config, *daemonMode)
}
