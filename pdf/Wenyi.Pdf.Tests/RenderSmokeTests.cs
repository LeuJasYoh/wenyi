using System.Text;
using System.Text.Json;
using Xunit;
namespace Wenyi.Pdf.Tests;

public class RenderSmokeTests
{
    private static string MakeState(string dir, bool bilingual)
    {
        Directory.CreateDirectory(Path.Join(dir, "chapters"));
        var manifest = new
        {
            title = "サンプル小説",
            fmt = "text",
            source_lang = "ja",
            target_lang = "zh",
            initialized = true,
            meta = new { },
            chapters = new object[]
            {
                new { index = 0, title = "第一章　出会い", toc_entry_id = (string?)null, status = "done" },
                new { index = 1, title = "第二章　放課後", toc_entry_id = (string?)null, status = "done" },
            },
        };
        File.WriteAllText(Path.Join(dir, "manifest.json"), JsonSerializer.Serialize(manifest, new JsonSerializerOptions { WriteIndented = true }), Encoding.UTF8);
        void WriteChapter(int index, string title, string translated, string[] sources, string[] targets)
        {
            var segments = new List<object>();
            var idx = 0;
            segments.Add(new { index = idx++, source = title, kind = "heading", target = (string?)translated, anchor = (string?)null, resource_href = (string?)null, cont = false, meta = new { } });
            for (var i = 0; i < sources.Length; i++)
            {
                segments.Add(new { index = idx++, source = sources[i], kind = "text", target = (string?)targets[i], anchor = (string?)null, resource_href = (string?)null, cont = false, meta = new { } });
            }
            var chapter = new
            {
                index,
                title,
                segments,
                href = (string?)null,
                template = (string?)null,
                meta = new { },
            };
            File.WriteAllText(Path.Join(dir, "chapters", $"ch{index}.json"), JsonSerializer.Serialize(chapter, new JsonSerializerOptions { WriteIndented = true }), Encoding.UTF8);
        }
        WriteChapter(0, "第一章　出会い", "第一章 相遇",
            new[] { "綾小路は教室の窓際に座っていた。", "「おはよう」と堀北が声をかけた。" },
            new[] { "绫小路坐在教室的窗边。", "“早上好。”堀北打招呼道。" });
        WriteChapter(1, "第二章　放課後", "第二章 放学后",
            new[] { "放課後、二人は屋上で待ち合わせた。" },
            new[] { "放学后，两人在天台碰面。" });
        return dir;
    }

    [Fact]
    public void Placeholder()
    {
        Assert.True(true);
    }

    [Fact]
    public void RenderProducesPdfWithCjk()
    {
        var dir = Path.Combine(Path.GetTempPath(), $"wenyi-pdf-test-{Guid.NewGuid():N}");
        Directory.CreateDirectory(dir);
        try
        {
            MakeState(dir, bilingual: false);
            var outPath = Path.Join(dir, "out", "book.pdf");
            var code = Program.Run(new[] { "render", "--state-dir", dir, "--out", outPath });
            Assert.Equal(0, code);
            Assert.True(File.Exists(outPath), "PDF 文件应存在");
            var head = new byte[5];
            using (var fs = File.OpenRead(outPath))
            {
                fs.ReadExactly(head, 0, 5);
            }
            Assert.Equal("%PDF-", Encoding.ASCII.GetString(head));
            Assert.True(new FileInfo(outPath).Length > 10_000, "含 CJK 字体的 PDF 不应过小");
        }
        finally
        {
            Directory.Delete(dir, true);
        }
    }

    [Fact]
    public void MissingStateDirFails()
    {
        var code = Program.Run(new[] { "render", "--state-dir", Path.Combine(Path.GetTempPath(), $"nope-{Guid.NewGuid():N}"), "--out", "x.pdf" });
        Assert.Equal(1, code);
    }
}
