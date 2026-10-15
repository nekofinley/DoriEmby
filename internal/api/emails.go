package api

import (
	"fmt"
	"strings"
	"time"
)

// The two messages this server sends. Both carry the link in plain text as
// well as HTML: some clients refuse HTML, and a link nobody can click is a
// dead end for the account.

func verificationMail(name, link string, ttl time.Duration) (subject, html, text string) {
	subject = "验证你的邮箱地址 · DoriyaMedia"
	html = mailLayout("确认你的邮箱地址", fmt.Sprintf(`
		<p style="margin:0 0 14px">%s,你好:</p>
		<p style="margin:0 0 14px">有人用这个邮箱在 DoriyaMedia 注册了账号。点下面的按钮完成验证,就可以登录了。</p>
		<p style="margin:22px 0"><a href="%s" style="%s">验证邮箱地址</a></p>
		<p style="margin:0 0 14px;color:#8b93a1;font-size:13px">链接 %s 内有效,只能使用一次。如果按钮点不开,把下面这行复制到浏览器:</p>
		<p style="margin:0 0 14px;word-break:break-all;font-size:13px;color:#8b93a1">%s</p>
		<p style="margin:18px 0 0;color:#8b93a1;font-size:13px">不是你本人注册的?忽略这封邮件即可,账号在验证之前无法登录。</p>`,
		escapeHTML(name), link, mailButtonStyle(), humanTTL(ttl), escapeHTML(link)))
	text = fmt.Sprintf(`%s,你好:

有人用这个邮箱在 DoriyaMedia 注册了账号。打开下面的链接完成验证,就可以登录了。

%s

链接 %s 内有效,只能使用一次。
不是你本人注册的?忽略这封邮件即可,账号在验证之前无法登录。
`, name, link, humanTTL(ttl))
	return subject, html, text
}

func resetMail(name, link string, ttl time.Duration) (subject, html, text string) {
	subject = "重设你的密码 · DoriyaMedia"
	html = mailLayout("重设密码", fmt.Sprintf(`
		<p style="margin:0 0 14px">%s,你好:</p>
		<p style="margin:0 0 14px">有人请求重设这个账号的密码。点下面的按钮设置新密码。</p>
		<p style="margin:22px 0"><a href="%s" style="%s">设置新密码</a></p>
		<p style="margin:0 0 14px;color:#8b93a1;font-size:13px">链接 %s 内有效,只能使用一次。如果按钮点不开,把下面这行复制到浏览器:</p>
		<p style="margin:0 0 14px;word-break:break-all;font-size:13px;color:#8b93a1">%s</p>
		<p style="margin:18px 0 0;color:#8b93a1;font-size:13px">不是你本人请求的?忽略这封邮件,密码不会改变。</p>`,
		escapeHTML(name), link, mailButtonStyle(), humanTTL(ttl), escapeHTML(link)))
	text = fmt.Sprintf(`%s,你好:

有人请求重设这个账号的密码。打开下面的链接设置新密码。

%s

链接 %s 内有效,只能使用一次。
不是你本人请求的?忽略这封邮件,密码不会改变。
`, name, link, humanTTL(ttl))
	return subject, html, text
}

func testMail(link string) (subject, html, text string) {
	subject = "邮件发送测试 · DoriyaMedia"
	html = mailLayout("邮件发送正常", fmt.Sprintf(`
		<p style="margin:0 0 14px">如果你读到这封邮件,说明 SMTP 配置可用,注册验证与找回密码都能正常发信。</p>
		<p style="margin:0 0 14px;color:#8b93a1;font-size:13px">这封邮件来自 %s 的「测试发送」按钮,不需要回复。</p>`,
		escapeHTML(link)))
	text = fmt.Sprintf("邮件发送正常。\n\n如果你读到这封邮件,说明 SMTP 配置可用,注册验证与找回密码都能正常发信。\n\n这封邮件来自 %s 的「测试发送」按钮,不需要回复。\n", link)
	return subject, html, text
}

// mailLayout wraps a body in the one table-free container every mail client
// renders predictably. Inline styles only: Gmail strips <style> blocks.
func mailLayout(title, body string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><body style="margin:0;padding:24px 12px;background:#f4f5f7;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',sans-serif;color:#1c1f26">
  <div style="max-width:520px;margin:0 auto;background:#ffffff;border:1px solid #e3e6ec;border-radius:14px;padding:28px">
    <div style="font-size:13px;font-weight:700;letter-spacing:.08em;text-transform:uppercase;color:#b8860b;margin-bottom:18px">DoriyaMedia</div>
    <h1 style="margin:0 0 18px;font-size:20px;line-height:1.4">%s</h1>
    <div style="font-size:14px;line-height:1.7">%s</div>
  </div>
</body></html>`, escapeHTML(title), body)
}

// mailButtonStyle is the shared call-to-action look. Kept in one place so the
// two templates cannot drift apart.
func mailButtonStyle() string {
	return "display:inline-block;padding:11px 22px;background:#f0a92e;color:#1a1206;text-decoration:none;border-radius:9px;font-weight:700;font-size:14px"
}

func humanTTL(ttl time.Duration) string {
	switch {
	case ttl >= 48*time.Hour:
		return fmt.Sprintf("%d 天", int(ttl.Hours()/24))
	case ttl >= 2*time.Hour:
		return fmt.Sprintf("%d 小时", int(ttl.Hours()))
	default:
		return fmt.Sprintf("%d 分钟", int(ttl.Minutes()))
	}
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}
