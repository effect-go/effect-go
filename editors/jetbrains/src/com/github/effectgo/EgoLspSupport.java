package com.github.effectgo;

import com.intellij.execution.configurations.GeneralCommandLine;
import com.intellij.execution.configurations.PathEnvironmentVariableUtil;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.platform.lsp.api.LspServerSupportProvider;
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor;
import java.io.File;
import java.nio.file.Path;

/**
 * Starts `ego lsp` when a .ego file is opened. It uses the API that 2026.2
 * deprecated, since its replacement doesn't exist in earlier versions.
 */
@SuppressWarnings("deprecation")
public final class EgoLspSupport implements LspServerSupportProvider {
  @Override
  public void fileOpened(Project project, VirtualFile file, LspServerStarter starter) {
    if (isEgo(file)) starter.ensureServerStarted(new Descriptor(project));
  }

  static boolean isEgo(VirtualFile file) {
    return "ego".equals(file.getExtension());
  }

  /** Finds a command on the PATH, else where go install puts it. */
  static String find(String name) {
    File f = PathEnvironmentVariableUtil.findInPath(name);
    if (f != null) return f.getPath();
    String gobin = System.getenv("GOBIN");
    Path p = gobin != null && !gobin.isEmpty()
        ? Path.of(gobin, name)
        : Path.of(System.getProperty("user.home"), "go", "bin", name);
    return p.toFile().canExecute() ? p.toString() : name;
  }

  private static final class Descriptor extends ProjectWideLspServerDescriptor {
    Descriptor(Project project) {
      super(project, "effect-go");
    }

    @Override
    public boolean isSupportedFile(VirtualFile file) {
      return isEgo(file);
    }

    @Override
    public GeneralCommandLine createCommandLine() {
      return new GeneralCommandLine(find("ego"), "lsp", "-gopls", find("gopls"));
    }

    @Override
    public String getLanguageId(VirtualFile file) {
      return "ego";
    }
  }
}
