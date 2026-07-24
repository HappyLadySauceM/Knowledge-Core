import { expect, test } from "@playwright/test";

test("public knowledge flow", async ({ page }, testInfo) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "Knowledge Core" })).toBeVisible();
  const images = page.locator("article img");
  for (let index = 0; index < await images.count(); index += 1) {
    await images.nth(index).scrollIntoViewIfNeeded();
    await expect(images.nth(index)).toHaveJSProperty("complete", true);
    await expect.poll(() => images.nth(index).evaluate((image) => (image as HTMLImageElement).naturalWidth)).toBeGreaterThan(0);
  }
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1, name: "Knowledge Core" })).toBeVisible();
  await expect(page.getByRole("banner")).toBeVisible();
  await page.screenshot({ path: `test-results/home-${testInfo.project.name}.png` });
  await page.getByRole("link", { name: "浏览文库" }).click();
  await expect(page).toHaveURL(/\/knowledge/);
  await expect(page.getByRole("heading", { level: 1, name: "文库" })).toBeVisible();
  const firstArticle = page.locator("article").first();
  await expect(firstArticle).toBeVisible();
  await firstArticle.getByRole("link").click();
  await expect(page.locator(".prose-content")).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: `test-results/article-${testInfo.project.name}.png` });
});

test("mobile navigation exposes primary destinations", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "mobile");
  await page.goto("/");
  const menuButton = page.getByRole("button", { name: "打开菜单" });
  await expect(menuButton).toBeEnabled();
  await menuButton.click();
  const navigation = page.getByRole("navigation", { name: "移动端主导航" });
  await expect(navigation).toBeVisible();
  await expect(navigation.getByRole("link", { name: "关于", exact: true })).toBeVisible();
});
