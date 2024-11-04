## 天使动漫论坛自动签到打工

**项目描述：**

本项目是一个使用 Go 语言编写的自动化脚本，用于自动在天使动漫论坛（tsdm39.com）进行签到和打工以及自动抢红包任务，并通过 Telegram 机器人推送结果通知。绝大部分内容由gemini-1.5-pro-exp-0827模型完成。

**功能：**

1. **自动签到：** 每天凌晨 0 点自动执行签到。
2. **自动打工：** 根据间隔时间定时执行打工任务。
3. **自动抢红包：** 自动抢红包（当前仅支持水区）。
4. **Telegram 推送：** 将签到结果和打工结果以及抢红包结果推送到Telegram。
5. **后台运行 (可选)：** 可以选择以守护进程的方式运行程序。
6. **多账户：** 支持多账户执行任务。
7. **支持Github Ations：** 支持Github Ations定时执行签到任务。

**配置文件 (config.yaml)：**

程序使用 YAML 格式的配置文件 `config.yaml` 来存储账户信息和 Telegram 推送配置。

```yaml
account:
  - name: 账户1 
    cookie: 你的cookie
  - name: 账户2
    cookie: 你的cookie
push:
  bot_token: 你的bot token
  chat_id: 你的chat id
```

**编译程序：**

1. **安装 Go 语言环境：** 确保你的系统已安装 Go 语言环境。
2. **获取依赖库：** 使用 `go get` 命令安装所需的依赖库：
   ```bash
   go get github.com/PuerkitoBio/goquery
   go get github.com/go-yaml/yaml
   go get github.com/mmcdole/gofeed
   go get golang.org/x/net/html/charset
   go get golang.org/x/sync/errgroup
   ```
3. **编译程序：** 使用 `go build` 命令编译程序：
   ```bash
   go build
   ```
**命令行参数：**

`-c`：指定配置文件路径，默认为 `config.yaml`。

`-d`：以守护进程模式运行程序。

  - **示例：**
    - **使用默认配置文件前台运行：**
       ```bash
       ./TsdmTask 
       ```
    - **使用自定义配置文件后台运行：**
       ```bash
       ./TsdmTask -c /path/to/config.yaml -d
       ```
