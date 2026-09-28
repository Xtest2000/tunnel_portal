/*
 * EventToken.h —— 交叉编译用的最小垫片（shim）
 *
 * 背景：WebView2.h 里有 `#include "EventToken.h"`，这是 Windows SDK 的头文件。
 * mingw-w64（含 Zig 自带的那套头文件）并不提供它，于是在 Linux 上用 cgo 交叉
 * 编译 Windows 版时会报 `'EventToken.h' file not found`。
 *
 * Windows 上原生编译时，Windows SDK 里真正的那份会先被找到，这个垫片不起作用；
 * 只有从 Linux 交叉编译时才需要把本目录加进 CGO_CFLAGS（见 README 的构建小节）。
 */
#ifndef EVENTTOKEN_H_SHIM_INCLUDED
#define EVENTTOKEN_H_SHIM_INCLUDED

#ifndef __EventRegistrationToken_DEFINED__
#define __EventRegistrationToken_DEFINED__

typedef struct EventRegistrationToken
{
    __int64 value;
} EventRegistrationToken;

#endif /* __EventRegistrationToken_DEFINED__ */

#endif /* EVENTTOKEN_H_SHIM_INCLUDED */
