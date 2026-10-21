#!/usr/bin/env python3
"""静态检查：metadata.json 字段、uuid 与目录名一致、XML 良构、JS 用到的 D-Bus 成员都在 XML 中声明，参数签名一致。"""
import json, os, re, sys, xml.etree.ElementTree as ET
d = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'wpd-shell@wpd.github.io')
errs = []
md = json.load(open(os.path.join(d, 'metadata.json')))
for k in ('uuid', 'name', 'description', 'shell-version'):
    if k not in md: errs.append(f'metadata 缺少 {k}')
if md.get('uuid') != os.path.basename(d): errs.append('uuid 与目录名不一致')
if not re.fullmatch(r'[-a-zA-Z0-9._]+@[-a-zA-Z0-9._]+', md.get('uuid', '')): errs.append('uuid 格式不符合 GNOME 规范')
sv = md.get('shell-version', [])
if not all(isinstance(v, str) and re.fullmatch(r'\d+', v) for v in sv): errs.append('shell-version 必须是只含主版本号的字符串（45+ 规则）')
for v in ('46', '48'):
    if v not in sv: errs.append(f'shell-version 未声明 {v}')
if not isinstance(md.get('version', 1), int): errs.append('version 必须是整数')
root = ET.parse(os.path.join(d, 'io.github.wpd.Shell1.xml')).getroot()
iface = root.find('interface')
methods = {m.get('name'): ''.join(a.get('type') for a in m.findall('arg') if a.get('direction', 'in') == 'in') for m in iface.findall('method')}
signals = {s.get('name'): ''.join(a.get('type') for a in s.findall('arg')) for s in iface.findall('signal')}
js = open(os.path.join(d, 'extension.js')).read()
for name, sig in re.findall(r"call\('(\w+)',\s*(?:GLib\.Variant\.new|new GLib\.Variant)\('\((.*?)\)'", js):
    if name not in methods: errs.append(f'JS 调用了 XML 中不存在的方法 {name}')
    elif methods[name] != sig: errs.append(f'{name} 签名不一致：JS ({sig}) vs XML ({methods[name]})')
for name in re.findall(r"connectSignal\('(\w+)'", js):
    if name not in signals: errs.append(f'JS 订阅了 XML 中不存在的信号 {name}')
for name in re.findall(r"\.(\w+)Remote\(", js):
    if name not in methods: errs.append(f'JS 调用了不存在的方法 {name}Remote')
used = set(re.findall(r"call\('(\w+)'", js)) | set(re.findall(r"\.(\w+)Remote\(", js))
unused = set(methods) - used
if unused: print('提示：XML 中未被扩展使用的方法：', sorted(unused))
missing_sig = set(signals) - set(re.findall(r"connectSignal\('(\w+)'", js))
if missing_sig: errs.append(f'扩展未处理的信号：{sorted(missing_sig)}')
print('\n'.join(errs) if errs else 'CHECK OK：metadata、XML、JS↔XML 方法/信号/签名一致')
sys.exit(1 if errs else 0)
