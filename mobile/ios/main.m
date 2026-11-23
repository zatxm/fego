#import "FegoMobile.h"

@interface FegoScene : UIResponder <UIWindowSceneDelegate>
@property(nonatomic, strong) UIWindow *window;
@end

@implementation FegoScene
- (void)scene:(UIScene *)scene
    willConnectToSession:(UISceneSession *)session
                 options:(UISceneConnectionOptions *)options {
    self.window = [[UIWindow alloc] initWithWindowScene:(UIWindowScene *)scene];
    self.window.rootViewController = FegoMobileViewController();
    [self.window makeKeyAndVisible];
}
@end

@interface FegoDelegate : UIResponder <UIApplicationDelegate>
@end
@implementation FegoDelegate
@end

int main(int argc, char **argv) {
    @autoreleasepool {
        return UIApplicationMain(argc, argv, nil, NSStringFromClass(FegoDelegate.class));
    }
}
