#!/sbin/sh
# 由 update-binary 在解压完成后 source 执行（不是 exec）。不要在此脚本末尾调用 exit。

SKIPUNZIP=0

ui_print " "
ui_print "  Codex CLI for Android"
ui_print "  ---------------------"
ui_print "  · 内置本机 LLM 网关（Responses -> chat/completions）"
ui_print "  · 网页控制台：可切换上游接口与模型"
ui_print "  · 装好后在网页里填上游接口 / API Key / 模型（端口 8787）"
ui_print "  · devctl：在手机上 SSH 控制服务器 / 云电脑"
ui_print " "
ui_print "  重启后自动下载 codex 二进制（约 157MB）"
ui_print "  手机终端执行 su -c codexctl web 可查看网页地址"
ui_print "  首次使用请在网页控制台「设置」里填接口 / Key / 模型"
ui_print " "

set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm_recursive "$MODPATH/system/bin" 0 0 0755 0755 u:object_r:system_file:s0
set_perm_recursive "$MODPATH/scripts" 0 0 0755 0755
for f in service.sh post-fs-data.sh uninstall.sh; do
  [ -f "$MODPATH/$f" ] && set_perm "$MODPATH/$f" 0 0 0755
done