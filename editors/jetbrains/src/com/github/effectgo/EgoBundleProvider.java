package com.github.effectgo;

import com.intellij.ide.plugins.IdeaPluginDescriptor;
import com.intellij.ide.plugins.PluginManagerCore;
import com.intellij.openapi.extensions.PluginId;
import java.util.List;
import org.jetbrains.plugins.textmate.api.TextMateBundleProvider;

/** Highlights .ego files with the grammar of the VS Code extension, shipped in the plugin's bundle directory. */
public final class EgoBundleProvider implements TextMateBundleProvider {
  @Override
  public List<PluginBundle> getBundles() {
    IdeaPluginDescriptor plugin = PluginManagerCore.getPlugin(PluginId.getId("com.github.effect-go"));
    if (plugin == null) return List.of();
    return List.of(new PluginBundle("effect-go", plugin.getPluginPath().resolve("bundle")));
  }
}
