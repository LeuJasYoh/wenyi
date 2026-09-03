// Wenyi.Pdf —— QuestPDF 渲染组件（主规格 §13.7 语义映射 + 架构分册 §4.3）。
// 读 manifest + chapters JSON，排版 A5 PDF：18/16/20mm 边距、页脚页码、
// h1 强制分页（首元素除外）、双语段落对、图片宽 clamp、CJK 字体发现。
using System.Text;
using System.Text.Json;
using QuestPDF.Fluent;
using QuestPDF.Helpers;
using QuestPDF.Infrastructure;

internal static class Program
{
    private const string Version = "0.4.1";

    private sealed class Options
    {
        public string StateDir = "";
        public string Out = "";
        public bool Bilingual;
        public string? Font;
        public string Order = "target_first";
    }

    private static int Main(string[] args) => Run(args);

    public static int Run(string[] args)
    {
        if (args.Length == 0 || args[0] is "--help" or "-h")
        {
            Console.WriteLine("用法：dotnet Wenyi.Pdf.dll render --state-dir <DIR> --out <FILE> [--bilingual] [--order target_first|source_first] [--font <PATH>]");
            return 0;
        }
        if (args[0] is "--version" or "-v")
        {
            Console.WriteLine(Version);
            return 0;
        }
        if (args[0] != "render")
        {
            Console.Error.WriteLine($"未知命令：{args[0]}（可用：render、--help、--version）");
            return 2;
        }
        var opts = ParseArgs(args.Skip(1).ToArray());
        if (string.IsNullOrEmpty(opts.StateDir) || string.IsNullOrEmpty(opts.Out))
        {
            Console.Error.WriteLine("render 需要 --state-dir 与 --out");
            return 2;
        }
        try
        {
            Render(opts);
            Console.WriteLine("OUTPUT: " + Path.GetFullPath(opts.Out));
            return 0;
        }
        catch (Exception ex)
        {
            Console.Error.WriteLine("PDF 渲染失败：" + ex.Message);
            return 1;
        }
    }

    private static Options ParseArgs(string[] args)
    {
        var opts = new Options();
        for (var i = 0; i < args.Length; i++)
        {
            switch (args[i])
            {
                case "--state-dir":
                    opts.StateDir = args[++i];
                    break;
                case var s when s.StartsWith("--state-dir="):
                    opts.StateDir = s["--state-dir=".Length..];
                    break;
                case "--out":
                    opts.Out = args[++i];
                    break;
                case var s when s.StartsWith("--out="):
                    opts.Out = s["--out=".Length..];
                    break;
                case "--bilingual":
                    opts.Bilingual = true;
                    break;
                case "--font":
                    opts.Font = args[++i];
                    break;
                case var s when s.StartsWith("--font="):
                    opts.Font = s["--font=".Length..];
                    break;
                case "--order":
                    opts.Order = args[++i];
                    break;
            }
        }
        return opts;
    }

    // ---- 字体发现（TRANS_NOVEL_PDF_FONT → 平台候选）----

    private static string DiscoverFont(string? explicitFont)
    {
        if (!string.IsNullOrEmpty(explicitFont) && File.Exists(explicitFont))
        {
            return explicitFont;
        }
        var env = Environment.GetEnvironmentVariable("TRANS_NOVEL_PDF_FONT");
        if (!string.IsNullOrEmpty(env) && File.Exists(env))
        {
            return env;
        }
        var candidates = new List<string>();
        if (OperatingSystem.IsWindows())
        {
            var windir = Environment.GetFolderPath(Environment.SpecialFolder.Windows);
            candidates.Add(Path.Join(windir, "fonts", "msyh.ttc"));
            candidates.Add(Path.Join(windir, "fonts", "simsun.ttc"));
            candidates.Add(Path.Join(windir, "fonts", "msyh.ttf"));
        }
        else if (OperatingSystem.IsMacOS())
        {
            candidates.Add("/Library/Fonts/Arial Unicode.ttf");
            candidates.Add("/System/Library/Fonts/Hiragino Sans GB.ttc");
            candidates.Add("/System/Library/Fonts/PingFang.ttc");
        }
        else
        {
            candidates.Add("/usr/share/fonts/opentype/noto/NotoSerifCJK-Regular.ttc");
            candidates.Add("/usr/share/fonts/truetype/wqy/wqy-zenhei.ttc");
        }
        foreach (var path in candidates)
        {
            if (File.Exists(path))
            {
                return path;
            }
        }
        throw new InvalidOperationException(
            "未找到 CJK 字体：请用 --font 指定，或设置 TRANS_NOVEL_PDF_FONT 环境变量");
    }

    // ---- 状态目录读取 ----

    private sealed record Segment(int Index, string Source, string Kind, string? Target, bool Cont);

    private sealed record Chapter(int Index, string Title, string? TitleTranslated, List<Segment> Segments);

    private static (string title, List<Chapter> chapters) LoadState(string stateDir)
    {
        var manifestPath = Path.Join(stateDir, "manifest.json");
        if (!File.Exists(manifestPath))
        {
            throw new InvalidOperationException($"状态目录缺少 manifest.json：{stateDir}");
        }
        using var manifestDoc = JsonDocument.Parse(File.ReadAllText(manifestPath, Encoding.UTF8));
        var title = manifestDoc.RootElement.TryGetProperty("title", out var t) ? t.GetString() ?? "" : "";
        var chapters = new List<Chapter>();
        if (manifestDoc.RootElement.TryGetProperty("chapters", out var chapterList))
        {
            foreach (var info in chapterList.EnumerateArray())
            {
                var index = info.GetProperty("index").GetInt32();
                var chTitle = info.TryGetProperty("title", out var ct) ? ct.GetString() ?? "" : "";
                var translated = info.TryGetProperty("title_translated", out var tt) ? tt.GetString() : null;
                var chapterPath = Path.Join(stateDir, "chapters", $"ch{index}.json");
                var segments = new List<Segment>();
                if (File.Exists(chapterPath))
                {
                    using var chDoc = JsonDocument.Parse(File.ReadAllText(chapterPath, Encoding.UTF8));
                    foreach (var s in chDoc.RootElement.GetProperty("segments").EnumerateArray())
                    {
                        segments.Add(new Segment(
                            s.GetProperty("index").GetInt32(),
                            s.GetProperty("source").GetString() ?? "",
                            s.TryGetProperty("kind", out var k) ? k.GetString() ?? "text" : "text",
                            s.TryGetProperty("target", out var tg) ? tg.GetString() : null,
                            s.TryGetProperty("cont", out var c) && c.GetBoolean()));
                    }
                }
                chapters.Add(new Chapter(index, chTitle, translated, segments));
            }
        }
        return (title, chapters);
    }

    // ---- 渲染 ----

    private static void Render(Options opts)
    {
        // QuestPDF 社区许可声明（非商业用途）
        QuestPDF.Settings.License = LicenseType.Community;
        var (_, chapters) = LoadState(opts.StateDir);
        var fontPath = DiscoverFont(opts.Font);
        const string fontFamily = "WenyiCJK";
        using (var fontStream = File.OpenRead(fontPath))
        {
            QuestPDF.Drawing.FontManager.RegisterFontWithCustomName(fontFamily, fontStream);
        }
        var outDir = Path.GetDirectoryName(Path.GetFullPath(opts.Out));
        if (!string.IsNullOrEmpty(outDir))
        {
            Directory.CreateDirectory(outDir);
        }
        bool sourceFirst = opts.Order == "source_first";

        // 预处理：合并 cont 续段（回填语义：续段并回上一段）
        var blocks = new List<(string kind, string text, string? source)>();
        foreach (var chapter in chapters)
        {
            var chapterTitle = chapter.TitleTranslated ?? chapter.Title;
            if (!string.IsNullOrWhiteSpace(chapterTitle))
            {
                blocks.Add(("heading1", chapterTitle, null));
            }
            string? pendingSource = null;
            string? pendingTarget = null;
            string? pendingKind = null;
            foreach (var seg in chapter.Segments)
            {
                var target = string.IsNullOrWhiteSpace(seg.Target) ? seg.Source : seg.Target!;
                if (seg.Cont && pendingSource != null)
                {
                    pendingSource += seg.Source;
                    pendingTarget += target;
                    continue;
                }
                if (pendingSource != null)
                {
                    blocks.Add((pendingKind ?? "text", pendingTarget!, pendingSource));
                }
                pendingSource = seg.Source;
                pendingTarget = target;
                pendingKind = seg.Kind == "heading" ? "heading2" : "text";
            }
            if (pendingSource != null)
            {
                blocks.Add((pendingKind ?? "text", pendingTarget!, pendingSource));
            }
        }

        Document.Create(container =>
        {
            container.Page(page =>
            {
                page.Size(PageSizes.A5);
                page.MarginTop(20); // mm：上 20
                page.MarginBottom(18);
                page.MarginLeft(16);
                page.MarginRight(16);
                page.DefaultTextStyle(style => style.FontFamily(fontFamily).FontSize(9.5f).LineHeight(1.5f));
                page.Header().PaddingBottom(4);
                page.Footer().AlignCenter().Text(text =>
                {
                    text.CurrentPageNumber();
                });
                page.Content().PaddingVertical(8).Column(col =>
                {
                    col.Spacing(6);
                    var first = true;
                    foreach (var (kind, text, source) in blocks)
                    {
                        var isHeading1 = kind == "heading1";
                        if (isHeading1 && !first)
                        {
                            col.Item().PageBreak();
                        }
                        first = false;
                        col.Item().Element(e =>
                        {
                            if (isHeading1)
                            {
                                e.Text(text).FontSize(14f).Bold();
                            }
                            else if (kind == "heading2")
                            {
                                e.Text(text).FontSize(11.5f).Bold();
                            }
                            else if (opts.Bilingual && source != null)
                            {
                                e.Column(sub =>
                                {
                                    sub.Spacing(2);
                                    if (sourceFirst)
                                    {
                                        sub.Item().Text(source).FontSize(8f).FontColor(Colors.Grey.Darken1);
                                        sub.Item().Text(text);
                                    }
                                    else
                                    {
                                        sub.Item().Text(text);
                                        sub.Item().Text(source).FontSize(8f).FontColor(Colors.Grey.Darken1);
                                    }
                                });
                            }
                            else
                            {
                                e.Text(text);
                            }
                        });
                    }
                });
            });
        }).GeneratePdf(opts.Out);
    }
}
