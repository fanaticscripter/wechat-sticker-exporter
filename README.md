# 微信表情包导出工具

本工具在macOS上导出微信里保存的表情包图片。

## 原理

微信（Mac App Store版）会生成一个plist：`~/Library/Containers/com.tencent.xinWeChat/Data/Library/Application Support/com.tencent.xinWeChat/<version>/<hex_id>/Stickers/fav.archive`，其中`<version>`如`2.0b4.0.9`，`<hex_id>`应该是对应微信账户的一个32位hex字符串。这是一个binary plist，用plutil转换为xml后包含了所有保存的表情包的ID和下载URL。

注意微信本身将下载的表情包存在同文件夹下的`Persistence`（保存的）和`NonPersistence`（未保存临时的）目录下，但全部加密了，没什么用。唯一的作用是`Persistence`目录下文件的mtime可以告诉我们每个表情包是什么时候下载的，这个信息`fav.archive`里没有。

综上，本工具读取表情包ID和URL后进行下载，并尽量把文件mtime改成相应加密文件的mtime，以便查找最新表情。

## 使用

`git clone`后

```
go build
./wechat-sticker-exporter
```

下载的图片保存在`data`目录下，可以用`ls -lt data`来按修改时间倒序排列。
