// UI strings. English is the default; Simplified Chinese for browsers that prefer it.

const en = {
	lang: 'en',
	connecting: 'Connecting to the share…',
	reconnecting: 'Connection lost. Reconnecting…',
	waitingHost: 'The sharer is offline. Waiting for them to come back…',
	viewOnly: 'View only',
	canType: 'You can type',
	viewOnlyHint: 'You are watching. The sharer has not allowed typing.',
	canTypeHint: 'Your keystrokes go straight to the shared terminal.',
	nowCanType: 'The sharer let you type.',
	nowViewOnly: 'The sharer switched to view only.',
	viewers: (n: number) => (n === 1 ? '1 viewer' : `${n} viewers`),
	timeLeft: (s: string) => `${s} left`,
	noExpiry: 'No time limit',
	endedTitle: 'Sharing has ended',
	ended: {
		stopped: 'The sharer stopped sharing.',
		expired: 'The share link expired.',
		terminal_ended: 'The shared session ended.',
		not_found: 'This share does not exist or has already ended.',
		host_lost: 'The sharer went offline and did not come back.',
		relay_lost: 'The sharer lost their connection to the relay.',
		core_lost: "The sharer's Herdr stopped.",
		bridge_shutdown: 'The sharer stopped sharing.',
		kicked: 'You were disconnected from the share.',
		full: 'This share already has its maximum number of viewers.',
		shutdown: 'The relay restarted. Ask the sharer for a new link.'
	} as Record<string, string>,
	endedOther: 'The share is no longer available.',
	badLinkTitle: 'This link does not work',
	e2ee: 'End-to-end encrypted',
	e2eeHint:
		'Only you and the sharer can read this session. The relay forwards encrypted data it cannot read.',
	landingTitle: 'herdr-share relay',
	landingBody:
		'This server relays shared Herdr terminal sessions. Open the full link you were given; it ends with a key after the # sign.',
	landingPrivacy: 'Sessions are end-to-end encrypted; this relay cannot read them.',
	version: 'Version',
	auth: 'Creating shares',
	authToken: 'requires a token',
	authOpen: 'open to anyone',
	source: 'Source code'
};

export type Strings = typeof en;

const zh: Strings = {
	lang: 'zh-CN',
	connecting: '正在连接分享…',
	reconnecting: '连接已断开，正在重新连接…',
	waitingHost: '分享者暂时离线，正在等待重新连接…',
	viewOnly: '只读',
	canType: '可操作',
	viewOnlyHint: '你正在观看，分享者没有开放输入。',
	canTypeHint: '你的输入会直接发送到被分享的终端。',
	nowCanType: '分享者已允许你操作。',
	nowViewOnly: '分享者已切换为只读。',
	viewers: (n: number) => `${n} 人在看`,
	timeLeft: (s: string) => `剩余 ${s}`,
	noExpiry: '不限时',
	endedTitle: '分享已结束',
	ended: {
		stopped: '分享者已停止分享。',
		expired: '分享链接已过期。',
		terminal_ended: '被分享的会话已结束。',
		not_found: '这个分享不存在或已经结束。',
		host_lost: '分享者离线后没有重新连接。',
		relay_lost: '分享者与中继服务的连接已断开。',
		core_lost: '分享者的 Herdr 已停止。',
		bridge_shutdown: '分享者已停止分享。',
		kicked: '你已被断开连接。',
		full: '观看人数已达上限。',
		shutdown: '中继服务已重启，请向分享者索取新链接。'
	},
	endedOther: '这个分享已不可用。',
	badLinkTitle: '链接无效',
	e2ee: '端到端加密',
	e2eeHint: '只有你和分享者能看到会话内容，中继服务只转发它无法解读的加密数据。',
	landingTitle: 'herdr-share 中继服务',
	landingBody: '这个服务用于转发 Herdr 终端会话分享。请打开你收到的完整链接，它的 # 后面带有密钥。',
	landingPrivacy: '会话内容端到端加密，中继服务无法读取。',
	version: '版本',
	auth: '创建分享',
	authToken: '需要 token',
	authOpen: '无需认证',
	source: '源代码'
};

export function pickStrings(langs: readonly string[]): Strings {
	return langs.some((l) => l.toLowerCase().startsWith('zh')) ? zh : en;
}

export const t: Strings = pickStrings(typeof navigator === 'undefined' ? [] : navigator.languages);

export function formatRemaining(seconds: number, s: Strings = t): string {
	const zhLang = s.lang === 'zh-CN';
	if (seconds < 60) return zhLang ? '不到 1 分钟' : 'under a minute';
	const m = Math.round(seconds / 60);
	if (m < 60) return zhLang ? `${m} 分钟` : `${m} min`;
	const h = Math.floor(m / 60);
	const rest = m % 60;
	if (zhLang) return rest ? `${h} 小时 ${rest} 分钟` : `${h} 小时`;
	return rest ? `${h} h ${rest} min` : `${h} h`;
}

export function endedText(reason: string, message?: string, s: Strings = t): string {
	return s.ended[reason] ?? message ?? s.endedOther;
}
