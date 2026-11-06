# 敏感词库目录

生产部署把本目录只读挂载到容器的 `/etc/anonchat/wordlist`，后端读取 `words.txt`。
请勿把真实敏感词库提交到仓库（`.gitignore` 已忽略本目录下的 `*.txt`），
在服务器上放置 `words.txt` 即可。

`words.txt` 格式（UTF-8）：

- 每行一条：`词<TAB>级别`；
- 级别取 `block`（命中即拦截）或 `mask`（命中后打码），省略时默认 `block`；
- `#` 开头为注释，空行忽略。

示例：

```
# 示例（无害占位）
测试违禁词<TAB>block
示例替换词<TAB>mask
```
