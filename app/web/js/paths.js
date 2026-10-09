// 存储位置的纯逻辑:算出「默认在哪个目录」「用户改过没有」。
// 启动器在 /app/status 里给的是原生路径(Windows 上是 C:\...),这里只做字符串比较。
// 纯函数,不碰 DOM,所以 webtest.mjs 能直接测。

// 去掉结尾的斜杠,免得 "D:\models\" 和 "D:\models" 被当成两处
function trimSlash(p) {
  return (p || '').replace(/[\\/]+$/, '');
}

/** 用的是哪个分隔符:按启动器报的路径猜,猜不到就用 /。 */
function sepOf(st) {
  const p = st.data_dir || st.default_data_dir || '';
  return p.includes('\\') && !p.includes('/') ? '\\' : '/';
}

/** 运行目录的"基准":没被启动参数钉死时是系统默认位置,钉死了就是那个指定位置。 */
function runBase(st) {
  return (st.data_dir_locked ? st.data_dir : st.default_data_dir) || st.data_dir;
}

/** 模型目录的默认值:运行目录下的 models。 */
export function modelDirDefault(st) {
  const base = trimSlash(runBase(st));
  return base ? base + sepOf(st) + 'models' : '';
}

/** 两个目录是不是同一处。Windows / macOS 上忽略大小写。 */
export function sameDir(a, b) {
  const x = trimSlash(a), y = trimSlash(b);
  if (!x || !y) return false;
  const mac = typeof navigator !== 'undefined' && /Mac/i.test(navigator.platform || '');
  return mac ? x === y : x.toLowerCase() === y.toLowerCase();
}

/** 用户改过存储位置没有(摘要上显示「默认在系统盘」还是「已自定义」)。
 *  运行目录被启动参数钉死时不算"改过" —— 那是外面定的,网页上也动不了。 */
export function customPaths(st) {
  const runChanged = !st.data_dir_locked && !sameDir(st.data_dir, st.default_data_dir);
  return runChanged || !sameDir(st.model_dir, modelDirDefault(st));
}

/** 摘要右侧那一句的 i18n 键:
 *  完全默认 → hintDefault;被启动参数钉死 → hintLocked;改过 → hintCustom。 */
export function pathsHintKey(st) {
  if (customPaths(st)) return 'paths.hintCustom';
  return st.data_dir_locked ? 'paths.hintLocked' : 'paths.hintDefault';
}
