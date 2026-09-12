// paste — progressive enhancement only. Every feature works without JS;
// this file only adds copy buttons, Ctrl+Enter submit, and syntax
// highlighting via the vendored highlight.js (no CDN).
(function () {
	'use strict';

	// --- Copy buttons ------------------------------------------------------

	var status = document.getElementById('copy-status');

	function setStatus(text) {
		if (status) {
			status.textContent = text;
		}
	}

	function copyFallback(text) {
		var ta = document.createElement('textarea');
		ta.value = text;
		ta.setAttribute('readonly', '');
		ta.style.position = 'fixed';
		ta.style.opacity = '0';
		document.body.appendChild(ta);
		ta.select();
		try {
			document.execCommand('copy');
		} catch (err) {
			// Clipboard unavailable; nothing more we can do.
		}
		document.body.removeChild(ta);
	}

	function flashCopied(btn) {
		var original = btn.textContent;
		btn.textContent = 'Copied';
		setStatus('Copied to clipboard');
		setTimeout(function () {
			btn.textContent = original;
			setStatus('');
		}, 1500);
	}

	function copyText(btn, text) {
		if (!text) {
			return;
		}
		if (navigator.clipboard && navigator.clipboard.writeText) {
			navigator.clipboard.writeText(text).then(
				function () {
					flashCopied(btn);
				},
				function () {
					copyFallback(text);
					flashCopied(btn);
				}
			);
		} else {
			copyFallback(text);
			flashCopied(btn);
		}
	}

	document.addEventListener('click', function (ev) {
		var btn = ev.target.closest('[data-copy],[data-copy-src]');
		if (!btn) {
			return;
		}
		var text;
		if (btn.hasAttribute('data-copy')) {
			text = btn.getAttribute('data-copy');
		} else {
			var src = document.getElementById(btn.getAttribute('data-copy-src'));
			text = src ? src.textContent : '';
		}
		copyText(btn, text);
	});

	// --- Ctrl+Enter submits the create form --------------------------------

	var form = document.getElementById('create-form');
	var content = document.getElementById('content');
	if (form && content) {
		content.addEventListener('keydown', function (ev) {
			if ((ev.ctrlKey || ev.metaKey) && ev.key === 'Enter') {
				ev.preventDefault();
				if (form.requestSubmit) {
					form.requestSubmit();
				} else {
					form.submit();
				}
			}
		});
	}

	// --- Syntax highlighting via vendored highlight.js ----------------------

	var code = document.getElementById('paste-content');
	if (code && code.tagName === 'CODE' && window.hljs) {
		if (code.className.indexOf('language-') !== -1) {
			window.hljs.highlightElement(code);
		} else {
			// Auto-detect: no language selected.
			code.innerHTML = window.hljs.highlightAuto(code.textContent).value;
		}
	}
})();
