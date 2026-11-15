import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location('helper', pathlib.Path(__file__).with_name('helper.py'))
helper = importlib.util.module_from_spec(spec)
spec.loader.exec_module(helper)

class PolicyTests(unittest.TestCase):
    def test_untrusted_policies_are_rejected(self):
        for policy in ({}, [{'port': True, 'mode': 'local'}], [{'port': 0, 'mode': 'local'}],
                       [{'port': 65536, 'mode': 'local'}], [{'port': '22; flush ruleset', 'mode': 'all'}],
                       [{'port': 8000, 'mode': 'local', 'command': 'bad'}],
                       [{'port': 8000, 'mode': 'bad'}],
                       [{'port': 8000, 'mode': 'local'}, {'port': 8000, 'mode': 'all'}]):
            with self.subTest(policy=policy), self.assertRaises(ValueError):
                helper.validate(policy)

    def test_local_and_network_are_dual_stack_and_preserve_other_tables(self):
        lan = [('192.0.2.10', '192.0.2.0/24', 4), ('2001:db8::1', '2001:db8::/64', 6)]
        body = helper.rules(1000, [{'port': 8000, 'mode': 'network'}, {'port': 8001, 'mode': 'local'}], lan, True)
        self.assertIn('delete table inet hamstack_1000', body)
        self.assertNotIn('flush ruleset', body)
        self.assertIn('tcp dport 8000 ip daddr 192.0.2.10 ip saddr 192.0.2.0/24 accept', body)
        self.assertIn('tcp dport 8000 ip6 daddr 2001:db8::1 ip6 saddr 2001:db8::/64 accept', body)
        self.assertIn('tcp dport 8001 iifname "lo" accept', body)
        self.assertIn('tcp dport 8000 drop', body)
        self.assertIn('tcp dport 8001 drop', body)
        self.assertNotIn('tcp dport 22', body)

    def test_all_removes_restrictions_and_no_lan_fails_closed(self):
        body = helper.rules(1000, [{'port': 8000, 'mode': 'all'}], [], False)
        self.assertNotIn('drop', body)
        self.assertNotIn('delete table', body)
        body = helper.rules(1000, [{'port': 8000, 'mode': 'network'}], [], False)
        self.assertIn('tcp dport 8000 drop', body)
        self.assertNotIn('ip saddr', body)

    def test_ip_text_is_validated(self):
        with self.assertRaises(ValueError):
            helper.rules(1000, [{'port': 8000, 'mode': 'network'}], [('bad; flush ruleset', '192.0.2.0/24', 4)], False)

if __name__ == '__main__':
    unittest.main()
